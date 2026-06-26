import { Compartment, EditorState, RangeSetBuilder } from '@codemirror/state'
import { Decoration, EditorView, ViewPlugin, keymap } from '@codemirror/view'
import { defaultKeymap, indentWithTab } from '@codemirror/commands'
import { yaml } from '@codemirror/lang-yaml'
import { basicSetup } from 'codemirror'
import YAML from 'js-yaml'
import { collectionExamples, testExamples } from './examples.generated.js'
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

function stripPolicyStatus(policyYaml) {
  const lines = policyYaml.split('\n')
  const statusLineIndex = lines.findIndex((line) => /^status:/.test(line))

  if (statusLineIndex === -1) {
    return policyYaml.replace(/\s+$/, '')
  }

  return lines.slice(0, statusLineIndex).join('\n').replace(/\s+$/, '')
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

function formatEvaluateResult(result) {
  const state = result.complianceState || 'Unknown'
  const lines = [`# ${state}`, '', '# Compliance messages:']

  for (const msg of result.messages ?? []) {
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

function getExportPolicyYaml() {
  const policy = stripPolicyStatus(policyEditor.state.doc.toString()).trimEnd()

  return policy ? `${policy}\n` : ''
}

let policyEditor
let resourcesEditor
let resultsEditor

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
  resultsEditor = createResultsEditor(
    document.getElementById('results-editor'),
    PLACEHOLDER_RESULTS,
  )

  populateExampleMenu()
  setupExampleMenu()

  if (initialState.loadedFromShare) {
    setShareStatus('Loaded policy and resources from link.')
  } else if (initialState.shareError) {
    setShareStatus(`Could not load share link: ${initialState.shareError}`, 'error')
  }

  document.getElementById('export-btn').addEventListener('click', async () => {
    const exportBtn = document.getElementById('export-btn')
    exportBtn.disabled = true

    try {
      const encoded = await encodeShareState({
        policy: getExportPolicyYaml(),
        resources: resourcesEditor.state.doc.toString(),
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

    try {
      const response = await fetch('/api/evaluate', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          policy: stripEvaluationTimestamps(policyEditor.state.doc.toString()),
          resources: resourcesEditor.state.doc.toString(),
        }),
      })

      const data = await response.json()

      if (!response.ok || data.error) {
        setResultsContent(
          resultsEditor,
          `# Error (${response.status})

${data.error ?? 'Unknown error'}
`,
          'error',
        )

        return
      }

      setResultsContent(
        resultsEditor,
        formatEvaluateResult(data),
        data.complianceState || 'Unknown',
      )

      setEditorContent(
        policyEditor,
        appendPolicyStatus(policyEditor.state.doc.toString(), data.status),
      )
    } catch (err) {
      setResultsContent(
        resultsEditor,
        `# Request failed

${err.message}
`,
        'error',
      )
    } finally {
      runBtn.disabled = false
    }
  })
}

initializeApp()
