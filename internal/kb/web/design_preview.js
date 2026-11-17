'use strict';

// Design preview for document code blocks.
//
//   ```html preview css=site.css width=375 theme=dark
//   <header class="...">…</header>
//   ```
//
// The block is posted (same-origin form, CSRF token as a field) to
// /api/design-preview, which answers with the block wrapped in a page under a
// CSP sandbox; the iframe here is sandboxed as well, so the markup never runs
// script or reaches this page, its cookies or the API. css= names a .css
// attachment of the document's project (e.g. the built Tailwind CSS) that the
// server inlines; without it only the markup's own inline styles apply.
// jsx/tsx blocks get a light conversion (className→class, {/* */} removed).
let designPreviewSeq = 0;
const designPreviewLangs = new Set(['html', 'htm', 'jsx', 'tsx']);
const designPreviewWidths = [['전체', 0], ['1280', 1280], ['768', 768], ['375', 375]];

function designPreviewAttrs(info) {
  const attrs = { preview: false };
  for (const token of String(info || '').trim().split(/\s+/)) {
    if (!token) continue;
    const eq = token.indexOf('=');
    if (eq < 0) { if (token === 'preview') attrs.preview = true; continue; }
    attrs[token.slice(0, eq).toLowerCase()] = token.slice(eq + 1).replace(/^["']|["']$/g, '');
  }
  return attrs;
}

// Enough of JSX to show static markup: attribute names, comments and
// className={`a ${x ? 'b' : ''}`} template literals (expressions dropped).
function designPreviewFromJSX(code) {
  return code
    .replace(/\{\/\*[\s\S]*?\*\/\}/g, '')
    .replace(/\bclassName=\{`([^`]*)`\}/g, (_, s) => 'class="' + s.replace(/\$\{[^}]*\}/g, ' ').replace(/\s+/g, ' ').trim() + '"')
    .replace(/\bclassName=\{(["'])([^"']*)\1\}/g, 'class="$2"')
    .replace(/\bclassName=/g, 'class=')
    .replace(/\bhtmlFor=/g, 'for=');
}

function designPreviewButton(label, onClick, pressed) {
  const b = document.createElement('button');
  b.type = 'button'; b.textContent = label;
  if (pressed !== undefined) b.setAttribute('aria-pressed', String(pressed));
  b.addEventListener('click', onClick);
  return b;
}

// Returns the preview element for a fenced block, or null to keep the plain code block.
function designPreviewBlock(lang, info, code, pre, options) {
  const attrs = designPreviewAttrs(info);
  lang = String(lang || '').toLowerCase();
  if (!attrs.preview || !designPreviewLangs.has(lang) || typeof document === 'undefined') return null;
  const markup = lang === 'jsx' || lang === 'tsx' ? designPreviewFromJSX(code) : code;
  const projectId = (options && options.projectId) || '';
  const name = 'design-preview-' + (++designPreviewSeq);
  let width = Number(attrs.width) || 0, dark = attrs.theme === 'dark', loaded = false;

  const box = document.createElement('div');
  box.className = 'design-preview';
  const bar = document.createElement('div');
  bar.className = 'design-preview-bar';
  const stage = document.createElement('div');
  stage.className = 'design-preview-stage';
  const frame = document.createElement('iframe');
  frame.name = name;
  frame.title = '디자인 미리보기';
  frame.setAttribute('sandbox', '');
  frame.setAttribute('referrerpolicy', 'no-referrer');
  stage.append(frame);
  pre.hidden = true;

  const note = document.createElement('span');
  note.className = 'design-preview-note';
  note.textContent = attrs.css ? 'CSS: ' + attrs.css : 'CSS 미지정 — 클래스 스타일은 css=<첨부 파일명>으로 빌드된 CSS를 지정해야 적용됩니다';

  function load() {
    loaded = true;
    if (typeof csrf === 'undefined' || !csrf) { note.textContent = '로그인 후 미리보기를 볼 수 있습니다.'; return; }
    const form = document.createElement('form');
    form.method = 'post'; form.action = '/api/design-preview'; form.target = name; form.hidden = true;
    for (const [key, value] of Object.entries({ csrf, html: markup, css: attrs.css || '', project_id: projectId, theme: dark ? 'dark' : 'light' })) {
      const input = document.createElement('input');
      input.type = 'hidden'; input.name = key; input.value = value;
      form.append(input);
    }
    document.body.append(form);
    form.submit();
    form.remove();
  }
  function applyWidth() {
    frame.style.width = width ? width + 'px' : '100%';
    widthButtons.forEach(([b, w]) => b.setAttribute('aria-pressed', String(w === width)));
  }

  const previewTab = designPreviewButton('미리보기', () => show(true), true);
  const codeTab = designPreviewButton('코드', () => show(false), false);
  function show(preview) {
    stage.hidden = !preview; pre.hidden = preview;
    previewTab.setAttribute('aria-pressed', String(preview)); codeTab.setAttribute('aria-pressed', String(!preview));
    if (preview && !loaded) load();
  }
  const tabs = document.createElement('span');
  tabs.className = 'design-preview-group';
  tabs.append(previewTab, codeTab);

  const widths = document.createElement('span');
  widths.className = 'design-preview-group';
  const widthButtons = designPreviewWidths.map(([label, w]) => {
    const b = designPreviewButton(label, () => { width = w; applyWidth(); }, false);
    b.title = w ? `화면 폭 ${w}px` : '패널 폭에 맞춤';
    widths.append(b);
    return [b, w];
  });
  const theme = designPreviewButton(dark ? '다크' : '라이트', () => {
    dark = !dark; theme.textContent = dark ? '다크' : '라이트'; theme.setAttribute('aria-pressed', String(dark));
    load();
  }, dark);
  theme.title = '라이트/다크 전환 (html 요소에 dark 클래스)';
  const reload = designPreviewButton('새로고침', load);

  bar.append(tabs, widths, theme, reload, note);
  box.append(bar, stage, pre);
  applyWidth();

  // Post only when the block scrolls into view, so long documents stay cheap.
  if (typeof IntersectionObserver === 'function') {
    const seen = new IntersectionObserver(entries => {
      if (!entries.some(e => e.isIntersecting)) return;
      seen.disconnect();
      if (!loaded && !stage.hidden) load();
    });
    seen.observe(box);
  } else {
    queueMicrotask(() => { if (box.isConnected) load(); });
  }
  return box;
}

if (typeof module !== 'undefined') module.exports = { designPreviewAttrs, designPreviewFromJSX };
