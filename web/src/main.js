import { Compartment, EditorState, RangeSetBuilder } from '@codemirror/state'
import { Decoration, EditorView, ViewPlugin, keymap } from '@codemirror/view'
import { defaultKeymap, indentWithTab } from '@codemirror/commands'
import { lintGutter, setDiagnostics } from '@codemirror/lint'
import { yaml } from '@codemirror/lang-yaml'
import { basicSetup } from 'codemirror'
import YAML from 'js-yaml'
import { collectionExamples, testExamples } from './examples.generated.js'
import {
  formatCombinedResults,
  hasLintErrors,
  lintPlaygroundInputs,
  lintPolicySpec,
  mergeLintIssues,
  stripPolicyStatus,
} from './lint.js'
import { evaluatePolicy, initDryrunWasm, isDryrunWasmReady } from './wasm.js'
import {
  SHARE_LINK_WARN_BYTES,
  buildShareUrl,
  clearShareStatus,
  decodeShareState,
  encodeShareState,
  formatShareSize,
  readShareHash,
  setShareStatus,
} from './share.js'

const SAMPLE_POLICY = `apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
metadata:
  name: policy-pod-example
spec:
  remediationAction: inform
  severity: low
  namespaceSelector:
    include: ["default"]
  object-templates:
    - complianceType: musthave
      objectDefinition:
        apiVersion: v1
        kind: Pod
        metadata:
          name: sample-nginx-pod
        spec:
          containers:
            - image: nginx:1.18.0
              name: nginx
              ports:
                - containerPort: 80
`

const SAMPLE_RESOURCES = `apiVersion: v1
kind: Pod
metadata:
  name: sample-nginx-pod
spec:
  containers:
    - image: nginx:1.18.0
      name: nginx
      ports:
        - containerPort: 80
`

const PLACEHOLDER_MAPPINGS = `# Additional API mappings (optional)
# Merged with built-in defaults on Simulate.
#
# Generate from a cluster with: dryrun generate
#
# - group: example.com
#   kind: Widget
#   plural: widgets
#   scope: namespace
#   singular: widget
#   version: v1
`

const PLACEHOLDER_RESULTS = `# Results will appear here after you click Simulate.

# Compliance messages:

# Diffs:
`

const resultsThemeCompartment = new Compartment()

const COMPLIANCE_STYLES = {
  Compliant: {
    background: 'var(--result-compliant-bg)',
    lineColor: 'var(--result-compliant-line)',
  },
  NonCompliant: {
    background: 'var(--result-noncompliant-bg)',
    lineColor: 'var(--result-noncompliant-line)',
  },
  Unknown: {
    background: 'var(--result-neutral-bg)',
    lineColor: 'var(--result-neutral-line)',
  },
  error: {
    background: 'var(--result-noncompliant-bg)',
    lineColor: 'var(--result-noncompliant-line)',
  },
  default: {
    background: 'var(--surface)',
    lineColor: 'var(--text)',
  },
}

function complianceResultsTheme(complianceState) {
  const style = COMPLIANCE_STYLES[complianceState] ?? COMPLIANCE_STYLES.default

  return EditorView.theme(
    {
      '&': {
        height: '100%',
        backgroundColor: style.background,
      },
      '.cm-content': { caretColor: 'transparent' },
      '.cm-line:first-child': {
        color: style.lineColor,
        fontWeight: '600',
      },
    },
    { dark: false },
  )
}

function diffLineClass(text) {
  if (text.startsWith('+++') || text.startsWith('---') || text.startsWith('@@')) {
    return 'cm-diff-header'
  }

  if (text.startsWith('+')) {
    return 'cm-diff-add'
  }

  if (text.startsWith('-')) {
    return 'cm-diff-remove'
  }

  return null
}

function buildDiffDecorations(view) {
  const builder = new RangeSetBuilder()
  let inDiffs = false

  for (let lineNo = 1; lineNo <= view.state.doc.lines; lineNo++) {
    const line = view.state.doc.line(lineNo)
    const text = line.text

    if (text === '# Diffs:') {
      inDiffs = true
      continue
    }

    if (!inDiffs || text.length === 0) {
      continue
    }

    const className = diffLineClass(text)
    if (className) {
      builder.add(line.from, line.from, Decoration.line({ class: className }))
    }
  }

  return builder.finish()
}

const diffSectionHighlighter = ViewPlugin.fromClass(
  class {
    constructor(view) {
      this.decorations = buildDiffDecorations(view)
    }

    update(update) {
      if (update.docChanged) {
        this.decorations = buildDiffDecorations(update.view)
      }
    }
  },
  { decorations: (plugin) => plugin.decorations },
)

function createEditor(parent, initialDoc, { readOnly = false, highlightYaml = true } = {}) {
  const extensions = [
    basicSetup,
    keymap.of([...defaultKeymap, indentWithTab]),
    lintGutter(),
    EditorView.theme({
      '&': { height: '100%' },
      '.cm-content': { caretColor: readOnly ? 'transparent' : undefined },
    }),
    EditorView.lineWrapping,
  ]

  if (highlightYaml) {
    extensions.splice(1, 0, yaml())
  }

  if (readOnly) {
    extensions.push(EditorState.readOnly.of(true))
  }

  return new EditorView({
    parent,
    state: EditorState.create({
      doc: initialDoc,
      extensions,
    }),
  })
}

function createResultsEditor(parent, initialDoc) {
  return new EditorView({
    parent,
    state: EditorState.create({
      doc: initialDoc,
      extensions: [
        basicSetup,
        keymap.of([...defaultKeymap, indentWithTab]),
        EditorState.readOnly.of(true),
        EditorView.lineWrapping,
        diffSectionHighlighter,
        resultsThemeCompartment.of(complianceResultsTheme(null)),
      ],
    }),
  })
}

function setEditorContent(view, text) {
  view.dispatch({
    changes: { from: 0, to: view.state.doc.length, insert: text },
  })
}

function issuesToDiagnostics(view, issues) {
  return issues.map((issue) => {
    const lineNo = Math.min(Math.max(issue.line, 1), view.state.doc.lines)
    const line = view.state.doc.line(lineNo)
    const from = line.from + Math.max(0, issue.column - 1)
    let to = issue.endColumn ? line.from + issue.endColumn - 1 : from + 1

    if (to <= from) {
      to = Math.min(from + 1, line.to)
    }

    return {
      from,
      to,
      severity: issue.severity,
      message: issue.message,
    }
  })
}

function applyLintDiagnostics(view, issues) {
  view.dispatch(setDiagnostics(view.state, issuesToDiagnostics(view, issues)))
}

function clearLintDiagnostics(view) {
  view.dispatch(setDiagnostics(view.state, []))
}

function applyPlaygroundLint(lintIssues) {
  applyLintDiagnostics(
    policyEditor,
    lintIssues.filter((issue) => issue.source === 'Policy'),
  )
  applyLintDiagnostics(
    resourcesEditor,
    lintIssues.filter((issue) => issue.source === 'Resources'),
  )
}

function stripEvaluationTimestamps(policyYaml) {
  let doc

  try {
    doc = YAML.load(policyYaml)
  } catch {
    return policyYaml
  }

  if (doc && typeof doc === 'object' && doc.status && typeof doc.status === 'object') {
    delete doc.status.lastEvaluated
    delete doc.status.lastEvaluatedGeneration
  }

  return `${YAML.dump(doc, { indent: 2, lineWidth: -1, noRefs: true }).trimEnd()}\n`
}

function appendPolicyStatus(policyYaml, status) {
  const specOnly = stripPolicyStatus(policyYaml)
  const statusYaml = YAML.dump(
    { status },
    { indent: 2, lineWidth: -1, noRefs: true },
  )
    .trimEnd()
    .replace(/^status:/, 'status: # (status will be replaced each run)')

  return `${specOnly}\n${statusYaml}\n`
}

function setResultsContent(view, text, complianceState = null) {
  const themeState = complianceState === 'error' ? 'error' : complianceState

  view.dispatch({
    changes: { from: 0, to: view.state.doc.length, insert: text },
    effects: resultsThemeCompartment.reconfigure(complianceResultsTheme(themeState)),
  })
}

function complianceMessagesForResult(result) {
  if (result.messages?.length > 0) {
    return result.messages
  }

  const historyMessage = result.status?.history?.[0]?.message
  if (historyMessage) {
    return [historyMessage]
  }

  return []
}

function formatEvaluateResult(result) {
  const state = result.complianceState || 'Unknown'
  const lines = [`# ${state}`, '', '# Compliance messages:']

  for (const msg of complianceMessagesForResult(result)) {
    lines.push(msg)
  }

  lines.push('', '# Diffs:')

  for (const relObj of result.status?.relatedObjects ?? []) {
    const obj = relObj.object ?? {}
    const metadata = obj.metadata ?? {}
    const name = metadata.namespace
      ? `${metadata.namespace}/${metadata.name}`
      : metadata.name ?? ''

    lines.push(`${obj.apiVersion} ${obj.kind} ${name}:`)

    const diff = relObj.properties?.diff
    if (diff) {
      lines.push(diff.replace(/\n$/, ''))
    }

    lines.push('')
  }

  return lines.join('\n')
}

async function resolveInitialState() {
  const encoded = readShareHash()
  if (!encoded) {
    return {
      policy: SAMPLE_POLICY,
      resources: SAMPLE_RESOURCES,
    }
  }

  try {
    const shared = await decodeShareState(encoded)

    return {
      policy: shared.policy,
      resources: shared.resources,
      additionalMappings: shared.additionalMappings ?? '',
      loadedFromShare: true,
    }
  } catch (err) {
    return {
      policy: SAMPLE_POLICY,
      resources: SAMPLE_RESOURCES,
      shareError: err.message,
    }
  }
}

function updateShareLoadStatus(state) {
  if (state.loadedFromShare) {
    const parts = ['policy', 'resources']
    if (state.additionalMappings) {
      parts.push('mappings')
    }
    setShareStatus(`Loaded ${parts.join(' and ')} from link.`)
  } else if (state.shareError) {
    setShareStatus(`Could not load share link: ${state.shareError}`, 'error')
  } else {
    clearShareStatus()
  }
}

function applyResolvedState(state) {
  setEditorContent(policyEditor, state.policy)
  setEditorContent(resourcesEditor, state.resources)
  setEditorContent(mappingsEditor, state.additionalMappings || PLACEHOLDER_MAPPINGS)
  clearLintDiagnostics(policyEditor)
  clearLintDiagnostics(resourcesEditor)
  setResultsContent(resultsEditor, PLACEHOLDER_RESULTS, null)
  updateShareLoadStatus(state)

  const toggle = document.getElementById('example-menu-toggle')
  toggle.textContent = 'Load an example…'
  closeExampleMenu()
}

function setupShareHashListener() {
  window.addEventListener('hashchange', () => {
    resolveInitialState().then(applyResolvedState)
  })
}

function getExportPolicyYaml() {
  const policy = stripPolicyStatus(policyEditor.state.doc.toString()).trimEnd()

  return policy ? `${policy}\n` : ''
}

function hasMappingEntries(text) {
  return /^\s*-\s+(group|Group):/m.test(text)
}

function getAdditionalMappingsYaml() {
  const text = mappingsEditor.state.doc.toString().trimEnd()
  if (!text || !hasMappingEntries(text)) {
    return ''
  }

  return `${text}\n`
}

function buildEvaluateRequestBody(policyText, resourcesText) {
  const body = {
    policy: stripEvaluationTimestamps(policyText),
    resources: resourcesText,
  }
  const additionalMappings = getAdditionalMappingsYaml()
  if (additionalMappings) {
    body.additionalMappings = additionalMappings
  }

  return body
}

let policyEditor
let resourcesEditor
let mappingsEditor
let resultsEditor

function setupClusterPaneTabs() {
  const tabs = {
    resources: {
      tab: document.getElementById('cluster-tab-resources'),
      hint: document.getElementById('cluster-hint-resources'),
      editor: resourcesEditor,
      editorMount: document.getElementById('resources-editor'),
    },
    mappings: {
      tab: document.getElementById('cluster-tab-mappings'),
      hint: document.getElementById('cluster-hint-mappings'),
      editor: mappingsEditor,
      editorMount: document.getElementById('mappings-editor'),
    },
  }

  function activate(name) {
    for (const [key, { tab, hint, editor, editorMount }] of Object.entries(tabs)) {
      const active = key === name
      tab.classList.toggle('is-active', active)
      tab.setAttribute('aria-selected', String(active))
      tab.tabIndex = active ? 0 : -1
      hint.classList.toggle('tab-hidden', !active)
      editorMount.classList.toggle('tab-hidden', !active)
      if (active) {
        requestAnimationFrame(() => {
          editor.focus()
        })
      }
    }
  }

  for (const [name, { tab }] of Object.entries(tabs)) {
    tab.addEventListener('click', () => activate(name))
  }

  tabs.resources.tab.addEventListener('keydown', (event) => {
    if (event.key === 'ArrowRight') {
      event.preventDefault()
      activate('mappings')
      tabs.mappings.tab.focus()
    }
  })

  tabs.mappings.tab.addEventListener('keydown', (event) => {
    if (event.key === 'ArrowLeft') {
      event.preventDefault()
      activate('resources')
      tabs.resources.tab.focus()
    }
  })
}

function createExampleGroup(group, groupExamples) {
  const details = document.createElement('details')
  details.className = 'example-group'

  const summary = document.createElement('summary')
  summary.textContent = group
  details.appendChild(summary)

  const list = document.createElement('ul')
  list.className = 'example-group-list'

  for (const example of groupExamples.sort((a, b) => a.label.localeCompare(b.label))) {
    const item = document.createElement('li')
    const button = document.createElement('button')
    button.type = 'button'
    button.className = 'example-option'
    button.textContent = example.label
    button.dataset.exampleId = example.id
    button.dataset.exampleSection = 'From Tests'
    item.appendChild(button)
    list.appendChild(item)
  }

  details.appendChild(list)

  return details
}

function createExampleSection(title, body) {
  const section = document.createElement('details')
  section.className = 'example-section'

  const summary = document.createElement('summary')
  summary.textContent = title
  section.appendChild(summary)

  const sectionBody = document.createElement('div')
  sectionBody.className = 'example-section-body'
  sectionBody.append(body)
  section.appendChild(sectionBody)

  return section
}

function populateExampleMenu() {
  const panel = document.getElementById('example-menu-panel')
  panel.replaceChildren()

  const collectionBody = document.createDocumentFragment()

  if (collectionExamples.length === 0) {
    const empty = document.createElement('p')
    empty.className = 'example-empty'
    empty.textContent = 'No curated examples yet.'
    collectionBody.append(empty)
  } else {
    const list = document.createElement('ul')
    list.className = 'example-group-list'

    for (const example of collectionExamples) {
      const item = document.createElement('li')
      const button = document.createElement('button')
      button.type = 'button'
      button.className = 'example-option'
      button.textContent = example.label
      button.dataset.exampleId = example.id
      button.dataset.exampleSection = 'Collection'
      item.appendChild(button)
      list.appendChild(item)
    }

    collectionBody.append(list)
  }

  panel.appendChild(createExampleSection('Collection', collectionBody))

  const testsBody = document.createDocumentFragment()
  const groups = new Map()

  for (const example of testExamples) {
    if (!groups.has(example.group)) {
      groups.set(example.group, [])
    }

    groups.get(example.group).push(example)
  }

  for (const [group, groupExamples] of [...groups.entries()].sort((a, b) =>
    a[0].localeCompare(b[0]),
  )) {
    testsBody.append(createExampleGroup(group, groupExamples))
  }

  panel.appendChild(createExampleSection('From Tests', testsBody))
}

function setExampleMenuOpen(open) {
  const toggle = document.getElementById('example-menu-toggle')
  const panel = document.getElementById('example-menu-panel')

  toggle.setAttribute('aria-expanded', open ? 'true' : 'false')
  panel.hidden = !open
}

function closeExampleMenu() {
  setExampleMenuOpen(false)
}

function setupExampleMenu() {
  const menu = document.querySelector('.example-menu')
  const toggle = document.getElementById('example-menu-toggle')
  const panel = document.getElementById('example-menu-panel')

  toggle.addEventListener('click', () => {
    setExampleMenuOpen(panel.hidden)
  })

  panel.addEventListener('click', (event) => {
    const button = event.target.closest('[data-example-id]')
    if (!button) {
      return
    }

    loadExample(button.dataset.exampleId)
    toggle.textContent = `${button.dataset.exampleSection} · ${button.textContent}`
    closeExampleMenu()
  })

  document.addEventListener('click', (event) => {
    if (!menu.contains(event.target)) {
      closeExampleMenu()
    }
  })

  document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape') {
      closeExampleMenu()
    }
  })
}

function findExample(exampleId) {
  return (
    collectionExamples.find((item) => item.id === exampleId) ??
    testExamples.find((item) => item.id === exampleId)
  )
}

function loadExample(exampleId) {
  const example = findExample(exampleId)
  if (!example) {
    return
  }

  setEditorContent(policyEditor, example.policy)
  setEditorContent(resourcesEditor, example.resources)
  setEditorContent(mappingsEditor, PLACEHOLDER_MAPPINGS)
  clearLintDiagnostics(policyEditor)
  clearLintDiagnostics(resourcesEditor)
  setResultsContent(resultsEditor, PLACEHOLDER_RESULTS, null)
  clearShareStatus()
}

async function initializeApp() {
  const initialState = await resolveInitialState()

  policyEditor = createEditor(document.getElementById('policy-editor'), initialState.policy)
  resourcesEditor = createEditor(
    document.getElementById('resources-editor'),
    initialState.resources,
  )
  mappingsEditor = createEditor(
    document.getElementById('mappings-editor'),
    initialState.additionalMappings || PLACEHOLDER_MAPPINGS,
  )
  resultsEditor = createResultsEditor(
    document.getElementById('results-editor'),
    PLACEHOLDER_RESULTS,
  )

  setupClusterPaneTabs()

  populateExampleMenu()
  setupExampleMenu()
  setupShareHashListener()

  if (initialState.loadedFromShare || initialState.shareError) {
    updateShareLoadStatus(initialState)
  }

  initDryrunWasm().catch(() => {
    // First Simulate will surface a clear error if the engine is missing.
  })

  document.getElementById('export-btn').addEventListener('click', async () => {
    const exportBtn = document.getElementById('export-btn')
    exportBtn.disabled = true

    try {
      const encoded = await encodeShareState({
        policy: getExportPolicyYaml(),
        resources: resourcesEditor.state.doc.toString(),
        additionalMappings: getAdditionalMappingsYaml(),
      })
      const url = buildShareUrl(encoded)
      const urlBytes = new TextEncoder().encode(url).length

      await navigator.clipboard.writeText(url)
      history.replaceState(null, '', url)

      if (urlBytes > SHARE_LINK_WARN_BYTES) {
        setShareStatus(
          `Link copied (${formatShareSize(urlBytes)}) — may be too long for some apps`,
          'warn',
        )
      } else {
        setShareStatus(`Link copied (${formatShareSize(urlBytes)})`)
      }
    } catch (err) {
      setShareStatus(`Could not copy share link: ${err.message}`, 'error')
    } finally {
      exportBtn.disabled = false
    }
  })

  document.getElementById('run-btn').addEventListener('click', async () => {
    const runBtn = document.getElementById('run-btn')
    runBtn.disabled = true

    let lintIssues = []
    let browserIssues = []

    try {
      if (!isDryrunWasmReady()) {
        setResultsContent(resultsEditor, '# Loading policy engine…\n', null)
      }

      const policyText = policyEditor.state.doc.toString()
      const resourcesText = resourcesEditor.state.doc.toString()
      browserIssues = lintPlaygroundInputs(policyText, resourcesText)
      const templateIssues = await lintPolicySpec(policyText)
      lintIssues = mergeLintIssues(browserIssues, templateIssues)

      applyPlaygroundLint(lintIssues)

      if (hasLintErrors(lintIssues)) {
        setResultsContent(resultsEditor, formatCombinedResults(lintIssues, ''), 'error')

        return
      }

      const requestBody = buildEvaluateRequestBody(policyText, resourcesText)
      const data = await evaluatePolicy(
        requestBody.policy,
        requestBody.resources,
        requestBody.additionalMappings ?? '',
      )

      setResultsContent(
        resultsEditor,
        formatCombinedResults(lintIssues, formatEvaluateResult(data)),
        data.complianceState || 'Unknown',
      )

      setEditorContent(
        policyEditor,
        appendPolicyStatus(policyEditor.state.doc.toString(), data.status),
      )
    } catch (err) {
      const issues = lintIssues.length ? lintIssues : browserIssues
      applyPlaygroundLint(issues)

      const status = err.status ?? 500
      const message = err.message ?? 'Unknown error'
      const errorHeading = status >= 500 ? 'Request failed' : `Error (${status})`

      setResultsContent(
        resultsEditor,
        formatCombinedResults(issues, `# ${errorHeading}\n\n${message}`),
        'error',
      )
    } finally {
      runBtn.disabled = false
    }
  })
}

initializeApp()
