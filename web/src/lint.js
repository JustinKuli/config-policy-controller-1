// Copyright Contributors to the Open Cluster Management project

import { parseAllDocuments, parseDocument } from 'yaml'

/**
 * @typedef {'error' | 'warning'} LintSeverity
 * @typedef {{ line: number, column: number, endColumn?: number, message: string, severity: LintSeverity, source: string }} LintIssue
 */

function issueFromError(error, source) {
  const start = error.linePos?.[0]
  const end = error.linePos?.[1]

  return {
    line: start?.line ?? 1,
    column: start?.col ?? 1,
    endColumn: end?.col,
    message: error.message.trim(),
    severity: 'error',
    source,
  }
}

function lineWithoutComment(line) {
  const hashIndex = line.indexOf('#')
  if (hashIndex === -1) {
    return line
  }

  return line.slice(0, hashIndex)
}

const UNQUOTED_TRUTHY = new Set(['y', 'n', 'yes', 'no', 'true', 'false', 'on', 'off'])

function isDocumentSeparator(line) {
  return /^\s*---(\s|$)/.test(line)
}

function collectStyleWarnings(text, source) {
  const issues = []
  const lines = text.split('\n')

  for (let index = 0; index < lines.length; index++) {
    const line = lines[index]
    const lineNo = index + 1
    const content = lineWithoutComment(line)

    const trailing = line.match(/[\t ]+$/)
    if (trailing && line.trim().length > 0) {
      issues.push({
        line: lineNo,
        column: line.length - trailing[0].length + 1,
        endColumn: line.length + 1,
        message: 'Trailing whitespace',
        severity: 'warning',
        source,
      })
    }

    const tabIndex = line.indexOf('\t')
    if (tabIndex !== -1) {
      issues.push({
        line: lineNo,
        column: tabIndex + 1,
        endColumn: tabIndex + 2,
        message: 'Tab character',
        severity: 'warning',
        source,
      })
    }

    if (!isDocumentSeparator(line)) {
      const hyphen = content.match(/^(\s*)-(\S)/)
      if (hyphen) {
        issues.push({
          line: lineNo,
          column: hyphen[1].length + 2,
          endColumn: hyphen[1].length + 3,
          message: 'Block sequence entries must start with "- "',
          severity: 'warning',
          source,
        })
      }
    }

    const mapping = content.match(/^(.*?):\s+(\S+)\s*$/)
    if (mapping) {
      const value = mapping[2]
      if (UNQUOTED_TRUTHY.has(value.toLowerCase())) {
        const valueStart = content.lastIndexOf(value) + 1
        issues.push({
          line: lineNo,
          column: valueStart,
          endColumn: valueStart + value.length,
          message: `Unquoted truthy value "${value}"`,
          severity: 'warning',
          source,
        })
      }
    }

    const listScalar = content.match(/^\s*-\s+(\S+)\s*$/)
    if (listScalar) {
      const value = listScalar[1]
      if (UNQUOTED_TRUTHY.has(value.toLowerCase())) {
        const valueStart = content.indexOf(value) + 1
        issues.push({
          line: lineNo,
          column: valueStart,
          endColumn: valueStart + value.length,
          message: `Unquoted truthy value "${value}"`,
          severity: 'warning',
          source,
        })
      }
    }
  }

  return issues
}

function collectDocumentErrors(doc, source) {
  return doc.errors.map((error) => issueFromError(error, source))
}

/**
 * Lint a single YAML document (policy pane).
 * @returns {LintIssue[]}
 */
export function lintYamlText(text, source) {
  const trimmed = text.trim()
  if (!trimmed) {
    return [
      {
        line: 1,
        column: 1,
        message: 'YAML document is empty',
        severity: 'error',
        source,
      },
    ]
  }

  const issues = []

  try {
    const doc = parseDocument(text, { strict: true })
    issues.push(...collectDocumentErrors(doc, source))
  } catch (error) {
    issues.push(issueFromError(error, source))
  }

  issues.push(...collectStyleWarnings(text, source))

  return issues
}

/**
 * Lint one or more YAML documents separated by --- (resources pane).
 * @returns {LintIssue[]}
 */
export function lintYamlDocuments(text, source) {
  const trimmed = text.trim()
  if (!trimmed) {
    return []
  }

  const issues = []

  try {
    const docs = parseAllDocuments(text, { strict: true })

    if (docs.length === 0) {
      issues.push({
        line: 1,
        column: 1,
        message: 'YAML document is empty',
        severity: 'error',
        source,
      })
    }

    for (const doc of docs) {
      issues.push(...collectDocumentErrors(doc, source))
    }
  } catch (error) {
    issues.push(issueFromError(error, source))
  }

  issues.push(...collectStyleWarnings(text, source))

  return issues
}

export function formatLintResults(issues) {
  if (issues.length === 0) {
    return ''
  }

  const lines = ['# Lint', '']

  for (const issue of issues) {
    const label = issue.severity === 'warning' ? 'warning' : 'error'
    lines.push(
      `${issue.source} ${label} (line ${issue.line}, col ${issue.column}): ${issue.message}`,
    )
  }

  return `${lines.join('\n')}\n`
}

export function formatCombinedResults(lintIssues, body) {
  const lintText = formatLintResults(lintIssues)
  const trimmedBody = body?.trimEnd() ?? ''

  if (lintText && trimmedBody) {
    return `${lintText.trimEnd()}\n\n${trimmedBody}\n`
  }

  if (lintText) {
    return lintText
  }

  return trimmedBody ? `${trimmedBody}\n` : ''
}

export function hasLintErrors(issues) {
  return issues.some((issue) => issue.severity === 'error')
}

export function lintPlaygroundInputs(policyText, resourcesText) {
  return [
    ...lintYamlText(policyText, 'Policy'),
    ...lintYamlDocuments(resourcesText, 'Resources'),
  ]
}
