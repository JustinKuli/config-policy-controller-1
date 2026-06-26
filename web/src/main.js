import { Compartment, EditorState, RangeSetBuilder } from '@codemirror/state'
import { Decoration, EditorView, ViewPlugin, keymap } from '@codemirror/view'
import { defaultKeymap, indentWithTab } from '@codemirror/commands'
import { yaml } from '@codemirror/lang-yaml'
import { basicSetup } from 'codemirror'
import YAML from 'js-yaml'

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

const PLACEHOLDER_RESULTS = `# Results will appear here after you run dryrun.

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

const policyEditor = createEditor(document.getElementById('policy-editor'), SAMPLE_POLICY)
const resourcesEditor = createEditor(
  document.getElementById('resources-editor'),
  SAMPLE_RESOURCES,
)
const resultsEditor = createResultsEditor(
  document.getElementById('results-editor'),
  PLACEHOLDER_RESULTS,
)

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
