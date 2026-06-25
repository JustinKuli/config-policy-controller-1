import { EditorState } from '@codemirror/state'
import { EditorView, keymap } from '@codemirror/view'
import { defaultKeymap, indentWithTab } from '@codemirror/commands'
import { yaml } from '@codemirror/lang-yaml'
import { basicSetup } from 'codemirror'

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

# Diffs:

# Compliance messages:
`

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

function setEditorContent(view, text) {
  view.dispatch({
    changes: { from: 0, to: view.state.doc.length, insert: text },
  })
}

const policyEditor = createEditor(document.getElementById('policy-editor'), SAMPLE_POLICY)
const resourcesEditor = createEditor(
  document.getElementById('resources-editor'),
  SAMPLE_RESOURCES,
)
const resultsEditor = createEditor(
  document.getElementById('results-editor'),
  PLACEHOLDER_RESULTS,
  { readOnly: true, highlightYaml: false },
)

document.getElementById('run-btn').addEventListener('click', () => {
  // Placeholder until the dryrun API is wired up.
  setEditorContent(
    resultsEditor,
    `# Dryrun API not connected yet.

Policy length: ${policyEditor.state.doc.length} characters
Resources length: ${resourcesEditor.state.doc.length} characters

Click "Run dryrun" again once the backend is wired up.
`,
  )
})
