# Policy Playground

Browser UI for the [dryrun](../pkg/dryrun/) CLI in this repository. Edit a
`ConfigurationPolicy`, provide simulated cluster resources, and view compliance
output in the browser.

Just a small word of warning: this playground was largely vibe-coded and may not
be an ideal representation of how to do this (especially the JS parts).

## Stack

- [Vite](https://vite.dev/) — dev server and production bundling
- Vanilla JavaScript (no framework)
- [CodeMirror 6](https://codemirror.net/) — YAML editors with syntax highlighting
- [@codemirror/lint](https://codemirror.net/) — inline lint markers on Simulate
- [js-yaml](https://github.com/nodeca/js-yaml) — policy/status serialization in the browser
- [yaml](https://eemeli.org/yaml/) — YAML parse and lint (eemeli)
- Go HTTP server in [`pkg/dryrun/server`](../pkg/dryrun/server/) — evaluation and
  [go-template-utils](https://github.com/stolostron/go-template-utils) policy template lint

## Prerequisites

Node.js **18, 20, or 22+** (Vite 6 requirement). Node **22** is recommended; see
`.nvmrc`.

```bash
cd web
nvm use
node -v
npm install
```

`npm install` and the first `npm run dev` / `npm run build` run
`scripts/generate-examples.mjs`, which writes `src/examples.generated.js` (gitignored).

## Building the dryrun binary

Run these from the **repository root**:

| Target | What it does |
|--------|----------------|
| `make generate-examples` | Regenerate `web/src/examples.generated.js` from example sources |
| `make build-web` | Runs `generate-examples`, then `npm run build` in `web/` → `web/dist/` |
| `make build-cmd` | Builds `build/_output/bin/dryrun` (CLI only; `serve` exposes the API, no embedded UI) |
| `make build-cmd-ui` | Runs `build-web`, then builds `dryrun` with the web UI embedded (`-tags embedui`) |

`make build-cmd-ui` requires `npm install` in `web/` first. The UI is embedded at
compile time via `go:embed` in [`web/embed.go`](embed.go) (active only with the
`embedui` build tag). `Dist()` strips the `dist/` prefix so static assets are
served from the filesystem root.

## Development

For live UI changes, run the API and Vite dev server as two processes:

```bash
# terminal 1 — from repository root; API on :8080
make build-cmd
build/_output/bin/dryrun serve --addr :8080

# terminal 2 — from web/
cd web
npm run dev
```

Open the URL Vite prints (usually `http://localhost:5173`). Vite proxies `/api` to
`:8080` (see `vite.config.js`).

If you use a UI-enabled binary (`make build-cmd-ui`) for the API process, pass
`--api-only` so it does not also serve the embedded pages:

```bash
build/_output/bin/dryrun serve --addr :8080 --api-only
```

## Production / integrated mode

Build one self-contained binary and serve everything from it:

```bash
make build-cmd-ui
build/_output/bin/dryrun serve --addr :8080
```

Open `http://localhost:8080`. No static directory or Node.js is required at runtime.

## API

The server exposes two JSON endpoints.

### Evaluate

```
POST /api/evaluate
Content-Type: application/json

{ "policy": "...", "resources": "..." }
```

Response on success (an `EvaluateResult`):

```json
{
  "complianceState": "Compliant",
  "status": { "compliant": "Compliant", "relatedObjects": [...] },
  "messages": ["..."]
}
```

Non-compliant policies still return `200` with the full result;
`complianceState` will be `NonCompliant`. Client errors (malformed YAML, invalid
resource field types, and similar input problems) return `400` with
`{ "error": "..." }`. Unexpected server failures return `500`.

The UI sends the full policy document (including any existing `status:` block) so
`history` accumulates across runs, but strips `status.lastEvaluated` and
`status.lastEvaluatedGeneration` before each request so evaluation is not skipped.

### Lint

```
POST /api/lint
Content-Type: application/json

{ "policy": "..." }
```

Response on success:

```json
{
  "issues": [
    {
      "line": 11,
      "column": 17,
      "severity": "warning",
      "message": "Templates should be single-quoted.",
      "ruleId": "GTUL003",
      "source": "Policy"
    }
  ]
}
```

Linting uses [go-template-utils `pkg/lint`](https://github.com/stolostron/go-template-utils/tree/main/pkg/lint).
The UI sends the policy spec only (no appended `status:` block). Malformed JSON
returns `400`; an empty issue list means no violations were found.

## Layout

```
┌──────────────────────────────┬──────────────────────────────┐
│ [logo] Policy Playground     │                              │
│              [Load example ▼]│ [Copy share link]  status    │
└──────────────────────────────┴──────────────────────────────┘
┌─────────────────────┬─────────────────────┐
│ Policy              │ Cluster resources   │
│ (YAML editor)       │ (YAML editor)       │
└─────────────────────┴─────────────────────┘
┌───────────────────────────────────────────┐
│ Results   [Simulate]                      │
│ (read-only output)                        │
└───────────────────────────────────────────┘
```

- **Policy Playground** — page title with Open Cluster Management logo.
- **Load an example…** — load a curated scenario or a `test/dryrun` case into both
  editors.
- **Copy share link** — gzip-compresses the current policy (spec only, no `status:`)
  and cluster resources into the page URL hash (`#s=…`), copies the link, and updates
  the address bar. Opening that link restores both editors. Links over ~8 KB show a
  warning that some chat or email tools may truncate them.
- **Policy** — the `ConfigurationPolicy` to evaluate. After a run, a `status:` section
  is appended below the spec (replaced on each run).
- **Cluster resources** — YAML documents simulating cluster state. Separate multiple
  objects with `---`.
- **Results** — lint output (when present), then compliance state, messages, and diffs.
  Read-only, line-wrapped, with diff-line coloring.

Editor panes use a fixed height (`--pane-height`, currently `70vh`) with internal
scrolling. Adjust that variable in `src/style.css` to change pane sizing globally.

## Simulate workflow

Clicking **Simulate** runs lint first, then calls the evaluation API:

1. **Lint** both editors in the browser, and lint the policy spec via `POST /api/lint`.
   Nothing runs while you type.
2. Show squiggles and gutter markers in the Policy and Cluster resources panes.
3. **Errors** (YAML syntax, empty policy, template delimiter/variable errors) block
   simulation and fill the Results pane with a `# Lint` section.
4. **Warnings** do not block simulation. They appear in Results alongside simulation
   output or API errors when present.
5. If template lint is unavailable (for example the API is unreachable), the UI shows
   a notice and continues with browser lint and evaluation.
6. **Evaluate** via `POST /api/evaluate` when there are no lint errors.
7. Append `status:` to the policy editor on success; lint markers on the spec are
   preserved. The appended status block is excluded from lint on subsequent runs.

### Lint rules

Policy and cluster resources use different linters. The generated `status:` block
appended after Simulate is never linted.

**Browser lint** ([`src/lint.js`](src/lint.js)) — YAML syntax and style:

| Severity | Source | Rule |
|----------|--------|------|
| Error | Policy, Resources | YAML syntax (strict parse), duplicate keys |
| Error | Policy | Empty policy document |
| Warning | Policy, Resources | Trailing whitespace |
| Warning | Policy, Resources | Tab characters |
| Warning | Policy, Resources | Block sequence entries not starting with `"- "` |
| Warning | Policy, Resources | Unquoted truthy scalars (`yes`, `no`, `on`, `off`, `true`, `false`, `y`, `n`) |

**Template lint** (`POST /api/lint`, go-template-utils) — policy spec only:

| Severity | Rule ID | Rule |
|----------|---------|------|
| Warning | GTUL001 | Trailing whitespace |
| Error | GTUL002 | Mismatched template delimiters (`{{`, `{{hub`, JSON `{}`) |
| Warning | GTUL003 | Unquoted template expressions in YAML values |
| Warning | GTUL004 | Unused template variables |
| Error | GTUL005 | Invalid template variable syntax |
| Warning | GTUL006 | Mismatched quotes around templates |

When both linters report trailing whitespace on the same policy line, the browser
warning is dropped in favor of the server result.

## Examples

Examples are generated at build time into `src/examples.generated.js`:

| Source | Menu section | How it is defined |
|--------|--------------|-------------------|
| `web/examples/*/` | **Collection** | Curated scenarios (one folder per example) |
| `test/dryrun/**/` | **From Tests** | Dryrun integration test cases |

**Curated example** — each folder under `web/examples/` contains:

- `policy.yaml` — must set `metadata.labels.description` (used as the menu label)
- `resources.yaml` — optional cluster objects

The example id is `collection/<folder-name>`.

**Test examples** — any `test/dryrun` directory with `policy.yaml` and `input*.yaml`
files. Scenarios with `error.txt`, `mappings.yaml`, or directory-based inputs are
skipped. Policy-wrapper comments are stripped when generating test examples.

```bash
npm run generate-examples   # or: make generate-examples
```

## Project structure

```
web/
├── index.html              # page shell and pane layout
├── embed.go                # go:embed dist/* (embedui build tag)
├── embed_stub.go           # no-op Dist() for builds without embedui
├── examples/               # curated example sources (one folder per example)
├── scripts/
│   └── generate-examples.mjs
├── src/
│   ├── main.js             # CodeMirror, API client, example menu, Simulate flow
│   ├── lint.js             # Browser lint, server lint client, result formatting
│   ├── share.js            # URL hash encode/decode and share status UI
│   ├── style.css
│   ├── ocm-logo-hept.png
│   └── examples.generated.js   # generated; gitignored
├── package.json
├── vite.config.js          # dev proxy for /api
└── .nvmrc
```

The default YAML shown on first load in `src/main.js` is based on
[`test/dryrun/ns_selector/ns_default/`](../test/dryrun/ns_selector/ns_default/).
