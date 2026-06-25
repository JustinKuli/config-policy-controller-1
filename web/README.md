# ConfigurationPolicy dryrun web UI

Browser UI for the [dryrun](../pkg/dryrun/) CLI in this repository. Edit a
`ConfigurationPolicy`, provide simulated cluster resources, and view dryrun output —
compliance status, diffs, and messages — in the browser.

This is a mostly-vibe-coded work in progress.

## Stack

- [Vite](https://vite.dev/) — dev server and production bundling
- Vanilla JavaScript (no framework)
- [CodeMirror 6](https://codemirror.net/) — YAML editors with syntax highlighting
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

## Building the dryrun binary

Run these from the **repository root**:

| Target | What it does |
|--------|----------------|
| `make build-web` | Runs `npm run build` in `web/` → creates `web/dist/` |
| `make build-cmd` | Builds `build/_output/bin/dryrun` (CLI only; `serve` exposes the API, no embedded UI) |
| `make build-cmd-ui` | Runs `build-web`, then builds `dryrun` with the web UI embedded (`-tags embedui`) |

`make build-cmd-ui` requires `npm install` in `web/` first. The UI is embedded at
compile time via `go:embed` in [`web/embed.go`](embed.go) (active only with the
`embedui` build tag).

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

Response on success:

```json
{ "output": "# Diffs:\n...", "complianceState": "Compliant" }
```

Non-compliant policies still return `200` with the full output;
`complianceState` will be `NonCompliant`. Parse errors return `400`.

## Layout

```
┌─────────────────────┬─────────────────────┐
│ Policy              │ Cluster resources   │
│ (YAML editor)       │ (YAML editor)       │
└─────────────────────┴─────────────────────┘
┌───────────────────────────────────────────┐
│ Results   [Run dryrun]                    │
│ (read-only output)                        │
└───────────────────────────────────────────┘
```

- **Policy** — the `ConfigurationPolicy` to evaluate.
- **Cluster resources** — YAML documents simulating cluster state. Separate multiple
  objects with `---`.
- **Results** — dryrun output (diffs, compliance messages). Read-only; scrolls
  internally when content is long.

Editor panes use a fixed height (`--pane-height`, currently `70vh`) with internal
scrolling. Adjust that variable in `src/style.css` to change pane sizing globally.

## Project structure

```
web/
├── index.html          # page shell and pane layout
├── embed.go            # go:embed dist/* (embedui build tag)
├── embed_stub.go       # no-op Dist() for builds without embedui
├── src/
│   ├── main.js         # CodeMirror setup and Run button handler
│   └── style.css       # layout and editor sizing
├── package.json
├── vite.config.js      # dev proxy for /api
└── .nvmrc
```

Sample YAML in `src/main.js` is taken from
[`test/dryrun/ns_selector/ns_default/`](../test/dryrun/ns_selector/ns_default/) in this
repository.
