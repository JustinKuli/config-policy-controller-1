// Copyright Contributors to the Open Cluster Management project

/** @typedef {{ evaluate: (policy: string, resources: string, mappings?: string) => string, lint: (policy: string) => string }} DryrunPlayground */

let initPromise = null
/** @type {DryrunPlayground | null} */
let playgroundReady = null

export function isDryrunWasmReady() {
  return playgroundReady !== null
}

function loadScript(src) {
  return new Promise((resolve, reject) => {
    const script = document.createElement('script')
    script.src = src
    script.addEventListener('load', () => resolve(), { once: true })
    script.addEventListener(
      'error',
      () => reject(new Error(`Failed to load ${src}`)),
      { once: true },
    )
    document.head.appendChild(script)
  })
}

/**
 * Load the dryrun WebAssembly module once.
 * @returns {Promise<DryrunPlayground>}
 */
export function initDryrunWasm() {
  if (!initPromise) {
    initPromise = loadDryrunWasm()
  }

  return initPromise
}

async function loadDryrunWasm() {
  await loadScript('/wasm_exec.js')

  if (typeof Go !== 'function') {
    throw new Error('Go WebAssembly runtime failed to initialize')
  }

  const go = new Go()
  const response = await fetch('/dryrun.wasm')

  if (!response.ok) {
    throw new Error(
      'Policy engine not found. Run `make build-wasm` from the repository root, then reload.',
    )
  }

  const result = await WebAssembly.instantiateStreaming(response, go.importObject)
  go.run(result.instance)

  const playground = await waitForPlaygroundExports()
  playgroundReady = playground
  return playground
}

async function waitForPlaygroundExports() {
  const deadline = Date.now() + 30_000

  while (Date.now() < deadline) {
    const playground = globalThis.dryrunPlayground
    if (playground?.evaluate && playground?.lint) {
      return playground
    }

    await new Promise((resolve) => setTimeout(resolve, 10))
  }

  throw new Error('Policy engine timed out while starting')
}

/**
 * @param {string} json
 * @returns {any}
 */
function parseWasmJson(json) {
  try {
    return JSON.parse(json)
  } catch {
    throw new Error('Policy engine returned invalid JSON')
  }
}

/**
 * @param {(playground: DryrunPlayground) => string} call
 * @returns {Promise<any>}
 */
async function callPlayground(call) {
  const playground = await initDryrunWasm()
  return parseWasmJson(call(playground))
}

/**
 * Evaluate a ConfigurationPolicy against simulated cluster resources.
 * @param {string} policy
 * @param {string} resources
 * @param {string} [additionalMappings]
 * @returns {Promise<any>}
 */
export async function evaluatePolicy(policy, resources, additionalMappings = '') {
  const data = await callPlayground((playground) =>
    playground.evaluate(policy, resources, additionalMappings ?? ''),
  )

  if (data.error) {
    const err = new Error(data.error)
    err.status = data.status ?? 500
    throw err
  }

  return data
}

/**
 * Lint policy template syntax via go-template-utils.
 * @param {string} policyYaml
 * @returns {Promise<Array<{ line: number, column: number, severity: string, message: string, ruleId?: string, source?: string }>>}
 */
export async function lintPolicyTemplates(policyYaml) {
  const data = await callPlayground((playground) => playground.lint(policyYaml))

  if (data.error) {
    throw new Error(data.error)
  }

  return data.issues ?? []
}
