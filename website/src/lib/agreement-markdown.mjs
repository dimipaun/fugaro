// website/src/lib/agreement-markdown.mjs — the Subscription Terms page's renderer.
//
// The page renders website/content/subscription-terms.md, which `go run ./internal/clidoc`
// cuts out of the draft in docs/licensing/. The agreement uses a small Markdown subset — "#"
// Part headings that group its "##" sections, paragraphs, bullet lists, one table, and code,
// bold, italic and links inline — and
// this renders that subset and refuses anything else: a construct it does not know would
// otherwise reach a reader as stray punctuation in a contract.

import { headingAnchor, headingText } from './github-slug.mjs'

/** @param {string} text */
function escapeHTML(text) {
  return text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')
}

/**
 * One line's inline markup as HTML.
 * @param {string} text
 * @param {(href: string) => string} link the target a document link renders with
 * @returns {string}
 */
export function inline(text, link) {
  let html = ''
  let plain = ''
  const flush = () => {
    html += escapeHTML(plain)
    plain = ''
  }
  let i = 0
  while (i < text.length) {
    const rest = text.slice(i)
    let m = null
    if (text[i] === '`' && (m = /^`([^`]+)`/.exec(rest))) {
      flush()
      html += `<code>${escapeHTML(m[1])}</code>`
    } else if (rest.startsWith('**') && (m = /^\*\*(.+?)\*\*/.exec(rest))) {
      flush()
      html += `<strong>${inline(m[1], link)}</strong>`
    } else if (text[i] === '*' && (m = /^\*([^*\s][^*]*)\*/.exec(rest))) {
      flush()
      html += `<em>${inline(m[1], link)}</em>`
    } else if (text[i] === '[' && (m = /^\[([^\]]*)\]\(([^)\s]+)\)/.exec(rest))) {
      flush()
      html += `<a href="${escapeHTML(link(m[2]))}" rel="noopener">${inline(m[1], link)}</a>`
    }
    if (m) {
      i += m[0].length
      continue
    }
    plain += text[i]
    i++
  }
  flush()
  if (/[*`]/.test(html.replace(/<code>[^<]*<\/code>/g, ''))) {
    throw new Error(`the Subscription Terms renderer left Markdown in ${JSON.stringify(text)}`)
  }
  return html
}

/** @param {string} line */
function tableCells(line) {
  return line.trim().replace(/^\|/, '').replace(/\|$/, '').split('|').map((cell) => cell.trim())
}

/**
 * The document's blocks, rendered in order.
 * @param {string} body the generated document below its header
 * @param {(href: string) => string} link
 * @returns {{ title: string, status: string, sections: { id: string, heading: string, html: string, part?: { id: string, heading: string } }[] }}
 */
export function renderAgreement(body, link) {
  const blocks = body.split(/\n[ \t]*\n/).map((block) => block.replace(/\s+$/, '')).filter((block) => block.trim() !== '')
  const title = /^# (.+)$/.exec(blocks.shift() ?? '')
  if (!title) throw new Error('the Subscription Terms document does not open with its "# " title')
  const status = blocks.shift() ?? ''
  if (/^[#|-]/.test(status)) throw new Error('the Subscription Terms document has no status line under its title')

  const sections = []
  // A Part heading opens the section that follows it, so a Part with no section is refused.
  let part
  for (const block of blocks) {
    const partHeading = /^# (Part [A-Z]\. .+)$/.exec(block)
    if (partHeading) {
      if (part) throw new Error(`the Subscription Terms document has a Part with no section: ${JSON.stringify(part.heading)}`)
      part = { id: headingAnchor(headingText(partHeading[1])), heading: inline(partHeading[1], link) }
      continue
    }
    const heading = /^## (.+)$/.exec(block)
    if (heading) {
      sections.push({ id: headingAnchor(headingText(heading[1])), heading: inline(heading[1], link), html: '', ...(part ? { part } : {}) })
      part = undefined
      continue
    }
    const section = sections.at(-1)
    if (!section) throw new Error(`the Subscription Terms document carries text before its first section: ${JSON.stringify(block.slice(0, 60))}`)
    if (/^#/.test(block)) throw new Error(`the Subscription Terms renderer knows only "# Part" headings and "##" sections: ${JSON.stringify(block.split('\n')[0])}`)

    const lines = block.split('\n')
    if (lines[0].startsWith('|')) {
      if (!lines.every((line) => line.startsWith('|')) || !/^\|[\s|:-]+\|$/.test(lines[1] ?? '')) {
        throw new Error(`the Subscription Terms renderer cannot read the table that starts ${JSON.stringify(lines[0])}`)
      }
      const head = tableCells(lines[0]).map((cell) => `<th scope="col">${inline(cell, link)}</th>`).join('')
      const rows = lines
        .slice(2)
        .map((line) => `<tr>${tableCells(line).map((cell) => `<td>${inline(cell, link)}</td>`).join('')}</tr>`)
        .join('')
      section.html += `<div class="terms-table"><table><thead><tr>${head}</tr></thead><tbody>${rows}</tbody></table></div>`
      continue
    }

    // A block may open with a paragraph and go on with a list, the way "There are three:"
    // introduces its plans.
    const paragraph = []
    const items = []
    for (const line of lines) {
      const bullet = /^- (.*)$/.exec(line)
      if (bullet) items.push(bullet[1])
      else if (items.length > 0 && /^\s+\S/.test(line)) items[items.length - 1] += ` ${line.trim()}`
      else if (items.length === 0) paragraph.push(line.trim())
      else throw new Error(`the Subscription Terms renderer cannot place ${JSON.stringify(line)} after a list`)
    }
    if (paragraph.length > 0) section.html += `<p>${inline(paragraph.join(' '), link)}</p>`
    if (items.length > 0) section.html += `<ul>${items.map((item) => `<li>${inline(item, link)}</li>`).join('')}</ul>`
  }
  if (part) throw new Error(`the Subscription Terms document has a Part with no section: ${JSON.stringify(part.heading)}`)
  return { title: title[1], status: inline(status, link), sections }
}
