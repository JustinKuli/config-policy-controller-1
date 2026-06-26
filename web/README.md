# Policy Playground

Browser UI for the [dryrun](../pkg/dryrun/) CLI in this repository. Edit a
`ConfigurationPolicy`, provide simulated cluster resources, and view compliance
output in the browser.

This is a mostly-vibe-coded work in progress.

## Stack

- [Vite](https://vite.dev/) — dev server and production bundling
- Vanilla JavaScript (no framework)
- [CodeMirror 6](https://codemirror.net/) — YAML editors with syntax highlighting
- [js-yaml](https://github.com/nodeca/js-yaml) — policy/status serialization in the browser
- Go HTTP server in [`pkg/dryrun/server`](../pkg/dryrun/server/) — calls `dryrun.Evaluate()`

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

The server exposes a single evaluation endpoint:

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
`complianceState` will be `NonCompliant`. Parse errors return `400` with
`{ "error": "..." }`.

The UI sends the full policy document (including any existing `status:` block) so
`history` accumulates across runs, but strips `status.lastEvaluated` and
`status.lastEvaluatedGeneration` before each request so evaluation is not skipped.

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
- **Results** — compliance state, messages, and diffs. Read-only, line-wrapped, with
  diff-line coloring. **Simulate** runs evaluation via the API.

Editor panes use a fixed height (`--pane-height`, currently `70vh`) with internal
scrolling. Adjust that variable in `src/style.css` to change pane sizing globally.

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
│   ├── main.js             # CodeMirror, API client, example menu
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
