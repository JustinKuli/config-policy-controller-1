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
- Go WebAssembly (`dryrun.wasm`) — policy evaluation and
  [go-template-utils](https://github.com/stolostron/go-template-utils) template lint,
  compiled from [`cmd/dryrun-wasm`](../cmd/dryrun-wasm/) via [`pkg/dryrun/playground`](../pkg/dryrun/playground/)

The playground is fully static: no backend server is required at runtime. Evaluation
runs locally in the browser through the same dryrun logic used by the CLI.

## Prerequisites

Node.js **18, 20, or 22+** (Vite 6 requirement). Node **22** is recommended; see
`.nvmrc`.

Go **1.26+** (see `go.mod`) is required to build `dryrun.wasm`.

```bash
cd web
nvm use
node -v
npm install
```

`npm install` and the first `npm run dev` / `npm run build` run
`scripts/generate-examples.mjs`, which writes `src/examples.generated.js` (gitignored).
If `public/dryrun.wasm` is missing, `scripts/ensure-wasm.mjs` runs `make build-wasm`.

## Building

Run these from the **repository root**:

| Target | What it does |
|--------|----------------|
| `make generate-examples` | Regenerate `web/src/examples.generated.js` from example sources |
| `make build-wasm` | Build `web/public/dryrun.wasm` and copy `wasm_exec.js` |
| `make build-web` | Runs `generate-examples`, `build-wasm`, then `npm run build` in `web/` → `web/dist/` |
| `make build-cmd` | Builds `build/_output/bin/dryrun` (CLI; optional `serve` subcommand exposes a JSON API) |
| `make build-cmd-ui` | Runs `build-web`, then builds `dryrun` with the web UI embedded (`-tags embedui`) |

`make build-cmd-ui` requires `npm install` in `web/` first. The UI is embedded at
compile time via `go:embed` in [`web/embed.go`](embed.go) (active only with the
`embedui` build tag). `Dist()` strips the `dist/` prefix so static assets are
served from the filesystem root.

## Development

```bash
cd web
npm run dev
```

Open the URL Vite prints (usually `http://localhost:5173`). The first run builds
`dryrun.wasm` if it is not already present.

## Production / static hosting

Build and preview locally:

```bash
make build-web
cd web && npm run preview
```

Deploy the contents of `web/dist/` to any static host (GitHub Pages, S3, Netlify,
etc.). No Node.js or Go process is required at runtime.

The policy engine is shipped as `dryrun.wasm` (~130 MB uncompressed, ~20 MB
gzip-compressed). Browsers download it once on the first Simulate click and cache
it. Enable gzip or brotli on your host/CDN if possible.

## Integrated binary (optional)

You can still serve the playground from a single self-contained binary:

```bash
make build-cmd-ui
build/_output/bin/dryrun serve --addr :8080
```

Open `http://localhost:8080`. This embeds the same static assets from `web/dist/`.

## dryrun serve API (optional)

The CLI subcommand `dryrun serve` (see [`pkg/dryrun/server`](../pkg/dryrun/server/))
exposes the same evaluate/lint logic as JSON endpoints. The browser UI no longer
uses these, but they remain available for other clients:

```
POST /api/evaluate   { "policy", "resources", "additionalMappings"? }
POST /api/lint       { "policy" }
```

## Layout

```
┌──────────────────────────────┬──────────────────────────────┐
│ [logo] Policy Playground     │                              │
│              [Load example ▼]│ [Copy share link]  status    │
└──────────────────────────────┴──────────────────────────────┘
┌─────────────────────┬─────────────────────┐
│ Policy              │ Cluster             │
│ (YAML editor)       │ [Resources|Mappings]│
│                     │ (YAML editor)       │
└─────────────────────┴─────────────────────┘
┌───────────────────────────────────────────┐
│ Results   [Simulate]                      │
│ (read-only output)                        │
└───────────────────────────────────────────┘
```

- **Policy Playground** — page title with Open Cluster Management logo.
- **Load an example…** — load a curated scenario or a `test/dryrun` case into the
  policy and resources editors; resets the API mappings tab to its placeholder.
- **Copy share link** — gzip-compresses the current policy (spec only, no `status:`),
  cluster resources, and any additional API mappings into the page URL hash (`#s=…`),
  copies the link, and updates the address bar. Opening that link restores all three
  editors. Links over ~8 KB show a warning that some chat or email tools may truncate
  them. Mappings are omitted from the link when the tab is empty.
- **Policy** — the `ConfigurationPolicy` to evaluate. After a run, a `status:` section
  is appended below the spec (replaced on each run).
- **Cluster** — tabbed pane with **Resources** (YAML documents simulating cluster
  state; separate multiple objects with `---`) and **API mappings** (optional
  additional mappings merged with built-in defaults on Simulate; same format as
  `dryrun generate`).
- **Results** — lint output (when present), then compliance state, messages, and diffs.
  Read-only, line-wrapped, with diff-line coloring.

Editor panes use a fixed height (`--pane-height`, currently `70vh`) with internal
scrolling. Adjust that variable in `src/style.css` to change pane sizing globally.

## Simulate workflow

Clicking **Simulate** loads the policy engine (if needed), runs lint, then evaluates:

1. **Preload** — the WASM module begins loading when the page opens; the first
   Simulate shows “Loading policy engine…” if it is not ready yet.
2. **Lint** — browser YAML lint on both editors, then template lint on the policy
   spec via WebAssembly. Nothing runs while you type.
3. Show squiggles and gutter markers in the Policy and Cluster resources panes.
4. **Errors** (YAML syntax, empty policy, template delimiter/variable errors) block
   simulation and fill the Results pane with a `# Lint` section.
5. **Warnings** do not block simulation. They appear in Results alongside simulation
   output or errors when present.
6. **Evaluate** via WebAssembly when there are no lint errors. Sends
   `additionalMappings` when the API mappings tab contains mapping entries.
7. Append `status:` to the policy editor on success. The appended status block is
   excluded from lint on subsequent runs.

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

**Template lint** (WebAssembly, go-template-utils) — policy spec only:

| Severity | Rule ID | Rule |
|----------|---------|------|
| Warning | GTUL001 | Trailing whitespace |
| Error | GTUL002 | Mismatched template delimiters (`{{`, `{{hub`, JSON `{}`) |
| Warning | GTUL003 | Unquoted template expressions in YAML values |
| Warning | GTUL004 | Unused template variables |
| Error | GTUL005 | Invalid template variable syntax |
| Warning | GTUL006 | Mismatched quotes around templates |

When both linters report trailing whitespace on the same policy line, the browser
warning is dropped in favor of the template lint result.

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
├── public/                 # dryrun.wasm and wasm_exec.js (generated; gitignored)
├── scripts/
│   ├── generate-examples.mjs
│   └── ensure-wasm.mjs
├── src/
│   ├── main.js             # CodeMirror, example menu, Simulate flow
│   ├── lint.js             # Browser YAML lint and result formatting
│   ├── wasm.js             # WebAssembly loader and evaluate/lint calls
│   ├── share.js            # URL hash encode/decode and share status UI
│   ├── style.css
│   ├── ocm-logo-hept.png
│   └── examples.generated.js   # generated; gitignored
├── package.json
└── .nvmrc
```

The default YAML shown on first load in `src/main.js` is based on
[`test/dryrun/ns_selector/ns_default/`](../test/dryrun/ns_selector/ns_default/).
