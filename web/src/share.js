// Copyright Contributors to the Open Cluster Management project

export const SHARE_VERSION = 1
export const SHARE_HASH_PREFIX = 's='
/** Links longer than this may fail in chat apps, email, etc. */
export const SHARE_LINK_WARN_BYTES = 8000

export function formatShareSize(bytes) {
  if (bytes < 1024) {
    return `${bytes} B`
  }

  if (bytes < 1024 * 1024) {
    return `${(bytes / 1024).toFixed(1)} KB`
  }

  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

export function buildShareUrl(encodedPayload) {
  const { origin, pathname, search } = window.location

  return `${origin}${pathname}${search}#${SHARE_HASH_PREFIX}${encodedPayload}`
}

export async function encodeShareState({ policy, resources }) {
  const payload = JSON.stringify({
    v: SHARE_VERSION,
    policy,
    resources,
  })

  const compressed = await gzipString(payload)

  return bytesToBase64Url(compressed)
}

export async function decodeShareState(encodedPayload) {
  const bytes = base64UrlToBytes(encodedPayload)
  const json = await gunzipString(bytes)
  const data = JSON.parse(json)

  if (data?.v !== SHARE_VERSION) {
    throw new Error('Unsupported share link version')
  }

  if (typeof data.policy !== 'string' || typeof data.resources !== 'string') {
    throw new Error('Share link is missing policy or resources')
  }

  return {
    policy: data.policy,
    resources: data.resources,
  }
}

export function readShareHash() {
  const hash = window.location.hash.slice(1)
  if (!hash.startsWith(SHARE_HASH_PREFIX)) {
    return null
  }

  const payload = hash.slice(SHARE_HASH_PREFIX.length)
  if (!payload) {
    return null
  }

  return payload
}

export function setShareStatus(message, kind = 'info') {
  const status = document.getElementById('share-status')
  status.textContent = message
  status.classList.remove('share-status-warn', 'share-status-error')

  if (kind === 'warn') {
    status.classList.add('share-status-warn')
  } else if (kind === 'error') {
    status.classList.add('share-status-error')
  }
}

export function clearShareStatus() {
  setShareStatus('')
}

async function gzipString(text) {
  const stream = new Blob([text]).stream().pipeThrough(new CompressionStream('gzip'))

  return new Uint8Array(await new Response(stream).arrayBuffer())
}

async function gunzipString(bytes) {
  const stream = new Blob([bytes]).stream().pipeThrough(new DecompressionStream('gzip'))

  return await new Response(stream).text()
}

function bytesToBase64Url(bytes) {
  let binary = ''

  for (const byte of bytes) {
    binary += String.fromCharCode(byte)
  }

  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

function base64UrlToBytes(value) {
  const base64 = value.replace(/-/g, '+').replace(/_/g, '/')
  const pad = base64.length % 4
  const padded = pad ? base64 + '='.repeat(4 - pad) : base64
  const binary = atob(padded)
  const bytes = new Uint8Array(binary.length)

  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i)
  }

  return bytes
}
