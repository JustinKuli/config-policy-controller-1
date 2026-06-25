# ConfigurationPolicy dryrun web UI

Browser UI for the [dryrun](../pkg/dryrun/) CLI in this repository. Lets you edit a
`ConfigurationPolicy`, provide simulated cluster resources, and (eventually) view dryrun
output — compliance status, diffs, and messages — without leaving the page.

This is a mostly-vibe-coded work in progress. The frontend layout and editors are in place;
the backend API that calls dryrun is not wired up yet.

## Stack

- [Vite](https://vite.dev/) — dev server and production bundling
- Vanilla JavaScript (no framework)
- [CodeMirror 6](https://codemirror.net/) — YAML editors with syntax highlighting

## Prerequisites

Node.js **18, 20, or 22+** (Vite 6 requirement). Node **22** is recommended; see
`.nvmrc`.

```bash
cd web
nvm use
node -v
```

## Development

```bash
cd web
npm install
npm run dev
```

Open the URL Vite prints (usually `http://localhost:5173`).

## Production build

```bash
cd web
npm run build
npm run preview   # optional: serve the built output locally
```

Built assets land in `dist/`. A future Go server can embed that directory to serve the
UI alongside a dryrun API.

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
├── src/
│   ├── main.js         # CodeMirror setup and Run button handler
│   └── style.css       # layout and editor sizing
├── package.json
└── .nvmrc
```

Sample YAML in `src/main.js` is taken from
[`test/dryrun/ns_selector/ns_default/`](../test/dryrun/ns_selector/ns_default/) in this
repository.

## Next steps

- Add a Go HTTP API that runs `pkg/dryrun` in-process and returns status/output
- Connect the **Run dryrun** button to that API
- Embed the production build into the server binary
