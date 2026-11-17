'use strict';

// Minimal Markdown renderer for knowledge documents.
// Builds DOM nodes only (never innerHTML), so raw HTML in a document is shown as text
// and cannot run under the page's strict CSP. Links are limited to http(s) and mailto.
// Supported: headings, paragraphs (single newlines become line breaks), emphasis, strike,
// inline code, fenced code (```html preview → design preview), block quotes, nested lists, task items, tables, rules, links.
const markdownPatterns = {
  fence: /^ {0,3}(`{3,}|~{3,})\s*([\w+-]*)(.*)$/,
  heading: /^ {0,3}(#{1,6})\s+(.*?)(?:\s+#+)?\s*$/,
  rule: /^ {0,3}([-*_])(?:\s*\1){2,}\s*$/,
  item: /^(\s*)([-*+]|\d{1,9}[.)])\s+(.*)$/,
  quote: /^ {0,3}>\s?(.*)$/,
  tableSeparator: /^\s*\|?\s*:?-{3,}:?\s*(?:\|\s*:?-{3,}:?\s*)*\|?\s*$/,
};

function markdownNode(tag, cls, text) {
  const node = document.createElement(tag);
  if (cls) node.className = cls;
  if (text !== undefined) node.textContent = text;
  return node;
}

function markdownSafeHref(url) {
  try {
    const parsed = new URL(url);
    return ['http:', 'https:', 'mailto:'].includes(parsed.protocol) ? parsed.href : null;
  } catch { return null; }
}

function markdownLink(label, url, allowLinks, parent) {
  const href = allowLinks ? markdownSafeHref(url) : null;
  if (!href) { markdownInline(label, parent, false); return; }
  const a = markdownNode('a');
  a.href = href; a.target = '_blank'; a.rel = 'noopener noreferrer nofollow';
  markdownInline(label, a, false);
  parent.append(a);
}

// Groups: 1 escape, 2-3 code, 4-5 link, 6 autolink, 7 bare URL, 8-9 strong, 10 strike, 11-12 emphasis.
const markdownInlinePattern = /\\([\\`*_{}[\]()#+\-.!|~>])|(`+)([^`]|[^`][\s\S]*?[^`])\2(?!`)|\[([^\]\n]+)\]\(\s*<?([^\s<>()]+)>?(?:\s+"[^"]*")?\s*\)|<(https?:\/\/[^\s<>]+)>|(https?:\/\/[^\s<>]*[^\s<>.,;:!?)\]'"])|\*\*(?=\S)([\s\S]*?\S)\*\*|(?<!\w)__(?=\S)([\s\S]*?\S)__(?!\w)|~~(?=\S)([\s\S]*?\S)~~|\*(?=[^\s*])([\s\S]*?[^\s*])\*(?!\*)|(?<![\w_])_(?=[^\s_])([\s\S]*?[^\s_])_(?![\w_])/g;

function markdownInline(text, parent, allowLinks = true) {
  const pattern = new RegExp(markdownInlinePattern.source, 'g');
  let last = 0, m;
  while ((m = pattern.exec(text))) {
    if (m.index > last) parent.append(text.slice(last, m.index));
    last = pattern.lastIndex;
    if (m[1] !== undefined) parent.append(m[1]);
    else if (m[2] !== undefined) parent.append(markdownNode('code', '', m[3].replace(/^ (.+) $/, '$1')));
    else if (m[4] !== undefined) markdownLink(m[4], m[5], allowLinks, parent);
    else if (m[6] !== undefined || m[7] !== undefined) {
      const url = m[6] || m[7], href = allowLinks ? markdownSafeHref(url) : null;
      if (!href) { parent.append(url); continue; }
      const a = markdownNode('a', '', url);
      a.href = href; a.target = '_blank'; a.rel = 'noopener noreferrer nofollow';
      parent.append(a);
    }
    else {
      const [tag, inner] = m[8] !== undefined ? ['strong', m[8]] : m[9] !== undefined ? ['strong', m[9]] : m[10] !== undefined ? ['del', m[10]] : ['em', m[11] ?? m[12]];
      const node = markdownNode(tag);
      markdownInline(inner, node, allowLinks);
      parent.append(node);
    }
  }
  if (last < text.length) parent.append(text.slice(last));
}

function markdownParagraph(lines, parent) {
  const p = markdownNode('p');
  lines.forEach((line, i) => {
    if (i) p.append(markdownNode('br'));
    markdownInline(line.trim(), p);
  });
  parent.append(p);
}

function markdownCells(line) {
  let row = line.trim();
  if (row.startsWith('|')) row = row.slice(1);
  if (row.endsWith('|') && !row.endsWith('\\|')) row = row.slice(0, -1);
  return row.split(/(?<!\\)\|/).map(cell => cell.trim().replace(/\\\|/g, '|'));
}

function markdownTable(lines, start, parent) {
  const header = markdownCells(lines[start]);
  const align = markdownCells(lines[start + 1]).map(c => c.endsWith(':') ? (c.startsWith(':') ? 'center' : 'right') : '');
  const wrap = markdownNode('div', 'markdown-table'), table = markdownNode('table');
  const head = markdownNode('thead'), headRow = markdownNode('tr');
  header.forEach((cell, i) => { const th = markdownNode('th'); if (align[i]) th.style.textAlign = align[i]; markdownInline(cell, th); headRow.append(th); });
  head.append(headRow); table.append(head);
  const body = markdownNode('tbody');
  let i = start + 2;
  for (; i < lines.length && lines[i].includes('|') && lines[i].trim(); i++) {
    const tr = markdownNode('tr');
    const cells = markdownCells(lines[i]);
    header.forEach((_, c) => { const td = markdownNode('td'); if (align[c]) td.style.textAlign = align[c]; markdownInline(cells[c] ?? '', td); tr.append(td); });
    body.append(tr);
  }
  table.append(body); wrap.append(table); parent.append(wrap);
  return i;
}

function markdownIndent(line) { return line.match(/^\s*/)[0].replace(/\t/g, '    ').length; }

function markdownList(lines, start, parent) {
  const first = lines[start].match(markdownPatterns.item);
  const base = markdownIndent(first[1]), ordered = /\d/.test(first[2]);
  const list = markdownNode(ordered ? 'ol' : 'ul');
  if (ordered && parseInt(first[2], 10) !== 1) list.start = parseInt(first[2], 10);
  let i = start;
  while (i < lines.length) {
    const m = lines[i].match(markdownPatterns.item);
    if (!m || markdownIndent(m[1]) !== base || /\d/.test(m[2]) !== ordered) break;
    const contentIndent = base + m[2].length + 1, body = [m[3]];
    i++;
    while (i < lines.length) {
      const line = lines[i];
      if (!line.trim()) {
        let next = i + 1;
        while (next < lines.length && !lines[next].trim()) next++;
        if (next < lines.length && markdownIndent(lines[next]) > base) { body.push(''); i++; continue; }
        break;
      }
      const indent = markdownIndent(line);
      if (indent > base) { body.push(line.replace(/\t/g, '    ').slice(Math.min(indent, contentIndent))); i++; continue; }
      if (markdownStartsBlock(lines, i)) break;
      body.push(line.trim()); i++; // lazy paragraph continuation
    }
    const li = markdownNode('li');
    const task = body[0].match(/^\[([ xX])\]\s+(.*)$/);
    if (task) { li.className = 'markdown-task'; li.append(markdownNode('span', 'markdown-check', task[1] === ' ' ? '☐ ' : '☑ ')); body[0] = task[2]; }
    markdownBlocks(body, li);
    // Tight items (no blank lines): unwrap the leading paragraph so items keep list spacing.
    const lead = [...li.children].find(c => !c.classList.contains('markdown-check'));
    if (lead?.tagName === 'P' && !body.includes('')) lead.replaceWith(...lead.childNodes);
    list.append(li);
  }
  parent.append(list);
  return i;
}

function markdownStartsBlock(lines, i) {
  const line = lines[i];
  return markdownPatterns.fence.test(line) || markdownPatterns.heading.test(line) || markdownPatterns.rule.test(line) ||
    markdownPatterns.quote.test(line) || markdownPatterns.item.test(line) ||
    (line.includes('|') && i + 1 < lines.length && markdownPatterns.tableSeparator.test(lines[i + 1]));
}

function markdownBlocks(lines, parent) {
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (!line.trim()) { i++; continue; }
    const fence = line.match(markdownPatterns.fence);
    if (fence) {
      const code = [], closing = new RegExp('^ {0,3}' + fence[1][0] + '{' + fence[1].length + ',}\\s*$');
      i++;
      while (i < lines.length && !closing.test(lines[i])) code.push(lines[i++]);
      i++; // An unterminated fence (common in document chunks) runs to the end.
      const pre = markdownNode('pre'), codeNode = markdownNode('code', fence[2] ? 'language-' + fence[2] : '', code.join('\n'));
      pre.append(codeNode);
      // ```html preview blocks become a sandboxed design preview when design_preview.js is loaded.
      const preview = typeof designPreviewBlock === 'function' ? designPreviewBlock(fence[2], fence[3], code.join('\n'), pre, markdownOptions) : null;
      parent.append(preview || pre);
      continue;
    }
    const heading = line.match(markdownPatterns.heading);
    if (heading) { const h = markdownNode('h' + heading[1].length); markdownInline(heading[2], h); parent.append(h); i++; continue; }
    if (markdownPatterns.rule.test(line)) { parent.append(markdownNode('hr')); i++; continue; }
    if (markdownPatterns.quote.test(line)) {
      const quoted = [];
      while (i < lines.length && lines[i].trim()) {
        const q = lines[i].match(markdownPatterns.quote);
        quoted.push(q ? q[1] : lines[i]); i++;
      }
      const block = markdownNode('blockquote'); markdownBlocks(quoted, block); parent.append(block);
      continue;
    }
    if (markdownPatterns.item.test(line)) { i = markdownList(lines, i, parent); continue; }
    if (line.includes('|') && i + 1 < lines.length && markdownPatterns.tableSeparator.test(lines[i + 1])) { i = markdownTable(lines, i, parent); continue; }
    const paragraph = [line];
    i++;
    while (i < lines.length && lines[i].trim() && !markdownStartsBlock(lines, i)) paragraph.push(lines[i++]);
    markdownParagraph(paragraph, parent);
  }
}

// Options for the render in progress (rendering is synchronous): projectId lets
// design previews resolve css=<file> against that project's attachments.
let markdownOptions = {};

function renderMarkdown(source, cls, options) {
  const root = markdownNode('div', 'markdown' + (cls ? ' ' + cls : ''));
  markdownOptions = options || {};
  try { markdownBlocks(String(source ?? '').replace(/\r\n?/g, '\n').split('\n'), root); }
  finally { markdownOptions = {}; }
  return root;
}

if (typeof module !== 'undefined') module.exports = { renderMarkdown };
