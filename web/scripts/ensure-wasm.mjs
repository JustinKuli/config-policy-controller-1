// Copyright Contributors to the Open Cluster Management project

import { access } from 'node:fs/promises'
import { spawnSync } from 'node:child_process'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const scriptDir = path.dirname(fileURLToPath(import.meta.url))
const wasmPath = path.join(scriptDir, '../public/dryrun.wasm')
const repoRoot = path.join(scriptDir, '../..')

try {
  await access(wasmPath)
  process.exit(0)
} catch {
  console.log('dryrun.wasm not found; building with make build-wasm...')
}

const result = spawnSync('make', ['build-wasm'], {
  cwd: repoRoot,
  stdio: 'inherit',
})

process.exit(result.status ?? 1)
