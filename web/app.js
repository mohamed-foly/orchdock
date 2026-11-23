// The management window: lists items and controls them through the local API.
import {
  buildRows, buildHome, mainTag, counts, variantsOf, parseEnvText, envToText, parseTags, isActive, itemIcon, iconPart, initials, formatNext, scheduleSpec, tagIcon, BUILTIN_ICONS, matchScore,
} from './model.js';

const token = document.querySelector('meta[name="orchdock-token"]').content;
const $ = (id) => document.getElementById(id);

// Per-viewer preferences; storage may be unavailable (private mode, previews).
const prefs = {
  get(key, fallback) { try { return localStorage.getItem(`orchdock.${key}`) || fallback; } catch { return fallback; } },
  set(key, value) { try { localStorage.setItem(`orchdock.${key}`, value); } catch { /* not kept */ } },
};

const state = {
  items: [],
  tags: [], // tag definitions: {name, path, color, icon, autoIcon}
  kinds: [],
  views: [], // sections (saved searches)
  selectMode: false,
  picked: new Set(),
  groupBy: ['tag', 'kind', 'none'].includes(prefs.get('groupBy', 'tag')) ? prefs.get('groupBy', 'tag') : 'tag',
  mode: prefs.get('mode', ''), // 'home' or 'all'; empty: Home once there is something to show
  collapsed: {},
  query: '',
  selected: null, // item id shown in the drawer
  pending: {}, // item id -> true while an action runs
  editing: null, // item being edited, or null for a new one
};

// ---- API ---------------------------------------------------------------

async function api(method, path, body) {
  const res = await fetch(path, {
    method,
    headers: { 'X-OrchDock-Token': token, ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}) },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch { data = text; }
  if (!res.ok) {
    const msg = (data && data.error) || `${res.status} ${res.statusText}`;
    const err = new Error(msg);
    err.status = res.status;
    err.code = data && data.code;
    throw err;
  }
  return data;
}

// ---- small DOM helpers ---------------------------------------------------

function el(tag, attrs = {}, ...kids) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v === false || v == null) continue;
    if (k === 'class') node.className = v;
    else if (k.startsWith('on')) node.addEventListener(k.slice(2), v);
    else if (k === 'text') node.textContent = v;
    else node.setAttribute(k, v === true ? '' : v);
  }
  for (const kid of kids.flat()) {
    if (kid == null || kid === false) continue;
    node.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
  }
  return node;
}

function toast(message, kind = 'info', onclick = null) {
  const t = el('div', { class: `toast ${kind}${onclick ? ' clickable' : ''}`, role: kind === 'error' ? 'alert' : 'status', onclick: onclick ? () => { t.remove(); onclick(); } : null }, message);
  $('toasts').append(t);
  setTimeout(() => t.remove(), kind === 'error' ? 7000 : 3000);
}

function fail(err) {
  toast(err.message || String(err), 'error');
}

// ---- data ---------------------------------------------------------------

let refreshTimer = 0;
function scheduleRefresh() {
  clearTimeout(refreshTimer);
  refreshTimer = setTimeout(refresh, 120);
}

async function refresh() {
  try {
    const data = await api('GET', '/api/state');
    state.items = data.items || [];
    state.tags = data.tags || [];
    state.kinds = data.kinds || [];
    state.views = data.views || [];
    $('trash').textContent = data.trash ? `Trash (${data.trash})` : 'Trash';
    renderViews();
    render();
    if ($('tags-dialog').open) renderTags();
    if ($('sections-dialog').open) renderSections();
    if ($('kinds-dialog').open) renderKinds();
    if (state.selected) await loadDrawer();
  } catch (err) {
    fail(err);
  }
}

const itemById = (id) => state.items.find((i) => i.id === id);

// ---- list ---------------------------------------------------------------

const tagDef = (name) => state.tags.find((d) => d.name === name);
const mainTagOf = (item) => mainTag(item, state.tags);
const homeHasContent = () => state.views.length > 0 || state.items.some((i) => i.pinned && !i.hidden);
const currentMode = () => state.mode || (homeHasContent() ? 'home' : 'all');

function render() {
  const c = counts(state.items);
  $('summary').textContent = c.total ? `${c.total} items · ${c.running} running${c.failed ? ` · ${c.failed} failed` : ''}` : '';
  const mode = currentMode();
  for (const b of $('mode').querySelectorAll('button')) b.setAttribute('aria-checked', String(b.dataset.mode === mode));
  $('group-wrap').hidden = mode !== 'all';
  const rows = mode === 'home'
    ? buildHome(state.items, state.query, state.collapsed, { views: state.views, kinds: state.kinds })
    : buildRows(state.items, state.query, state.collapsed, { groupBy: state.groupBy, tags: state.tags, kinds: state.kinds });
  const list = $('list');
  list.replaceChildren();
  if (mode === 'home' && !rows.length) {
    list.append(el('div', { class: 'empty' },
      el('p', {}, state.query ? 'No pinned item matches your search.' : 'Home shows your pinned items and your sections.'),
      el('p', { class: 'hint-text' }, 'Pin an item with ☆ on its row, or search (for example tag:shop) and save the search as a section.'),
      el('button', { type: 'button', onclick: () => setMode('all') }, 'Show all items')));
    return;
  }
  if (mode === 'all' && !rows.some((r) => r.type === 'item' || r.collapsed)) {
    list.append(el('p', { class: 'empty' }, state.items.length ? 'No item matches your search.' : 'No items yet. Click “+ New” to add one.'));
    return;
  }
  const frag = document.createDocumentFragment();
  for (const row of rows) {
    frag.append(row.type === 'header' ? groupHeader(row) : itemRow(row.item));
  }
  list.append(frag);
}

function setMode(mode) {
  state.mode = mode;
  prefs.set('mode', mode);
  state.collapsed = {};
  render();
  renderViews();
}

function groupHeader(row) {
  const toggle = el('button', {
    class: 'group', type: 'button', 'aria-expanded': String(!row.collapsed),
    onclick: () => { state.collapsed[row.group] = !row.collapsed; render(); },
  }, row.collapsed ? '▸' : '▾', row.group === 'pinned' ? '★ Pinned' : row.label, el('span', { class: 'n' }, row.count));
  const def = row.tag ? tagDef(row.tag) : null;
  const tint = (def && def.color) || row.color || '';
  const head = el('div', { class: tint ? 'ghead tinted' : 'ghead', style: tint ? `--pc:${tint}` : null, 'data-group': row.group });
  if (def) head.append(iconEl({ primary: tagIcon(def) }));
  head.append(toggle);
  if (row.running || row.failed) {
    head.append(el('span', { class: 'gstats' },
      row.running ? el('span', { class: 'ok' }, `${row.running} running`) : null,
      row.running && row.failed ? ' · ' : null,
      row.failed ? el('span', { class: 'bad' }, `${row.failed} failed`) : null));
  }
  const acts = el('span', { class: 'gact' });
  if (row.count && (currentMode() === 'home' || state.groupBy !== 'none')) {
    const members = row.ids.map(itemById).filter((i) => i && i.kind !== 'link');
    const idle = members.filter((i) => !isActive(i));
    const live = members.filter((i) => isActive(i));
    acts.append(
      el('button', { type: 'button', disabled: !idle.length, title: `Start every idle item in ${row.label}`, onclick: () => groupAction(row.label, 'run', idle) }, '▶ Start all'),
      el('button', { type: 'button', disabled: !live.length, title: `Stop every running item in ${row.label}`, onclick: () => groupAction(row.label, 'stop', live) }, '■ Stop all'));
  }
  const menu = groupMenu(row);
  if (menu.length) {
    acts.append(el('button', {
      type: 'button', class: 'more', title: `More for ${row.label}`, 'aria-label': `More for ${row.label}`,
      onclick: (e) => { e.stopPropagation(); showMenu(e.currentTarget, menu); },
    }, '⋯'));
  }
  if (acts.childNodes.length) head.append(acts);
  return head;
}

/** Menu entries of a group header: section and tag settings. */
function groupMenu(row) {
  const out = [];
  if (row.viewId) {
    const v = state.views.find((x) => x.id === row.viewId);
    const i = state.views.indexOf(v);
    out.push({ label: 'Rename section…', run: () => renameSection(v) });
    if (i > 0) out.push({ label: 'Move up', run: () => moveSection(v, -1) });
    if (i < state.views.length - 1) out.push({ label: 'Move down', run: () => moveSection(v, 1) });
    out.push({ label: 'Remove section', hint: 'items stay', run: () => removeSection(v) });
  }
  if (row.tag) {
    out.push({ label: `Tag settings: ${row.tag}`, hint: 'folder, colour, icon', run: () => openTags(row.tag) });
    const q = `tag:${row.tag}`;
    if (!row.viewId && !state.views.some((v) => v.query.trim() === q)) {
      out.push({ label: 'Show on Home', hint: 'as a section', run: () => addSection(row.tag, q) });
    }
  }
  return out;
}

async function groupAction(label, action, items) {
  if (!items.length) return;
  for (const it of items) state.pending[it.id] = true;
  render();
  try {
    const { results } = await api('POST', '/api/bulk', { action, refs: items.map((i) => i.id) });
    const bad = results.filter((r) => !r.ok);
    if (bad.length) toast(`${label}: ${bad.length} of ${results.length} failed — ${bad[0].error}`, 'error');
    else toast(`${label}: ${action === 'run' ? 'started' : 'stopped'} ${results.length}.`);
  } catch (err) {
    fail(err);
  } finally {
    for (const it of items) delete state.pending[it.id];
    await refresh();
  }
}

/** The address of a service: its URL, or http://127.0.0.1:<port>. */
function serviceURL(item) {
  const s = item.service;
  if (!s || item.kind !== 'service') return '';
  if (s.url) return s.url.replaceAll('{port}', String(s.port || ''));
  return s.port ? `http://127.0.0.1:${s.port}/` : '';
}

// ---- icons --------------------------------------------------------------

// Images need the token header, so they are fetched once and shown as blob URLs.
const iconURLs = new Map(); // name -> Promise<url|''>
function iconURL(name) {
  if (!iconURLs.has(name)) {
    iconURLs.set(name, fetch(`/api/icons/${encodeURIComponent(name)}`, { headers: { 'X-OrchDock-Token': token } })
      .then((r) => (r.ok ? r.blob() : null)).then((b) => (b ? URL.createObjectURL(b) : '')).catch(() => ''));
  }
  return iconURLs.get(name);
}

function partEl(part) {
  if (part.type === 'asset') return el('img', { alt: '', src: part.src });
  if (part.type === 'mask') return el('span', { class: 'gl' }, el('i', { class: 'lm', style: `--m:url(${part.src})` }));
  if (part.type === 'file') {
    const img = el('img', { alt: '' });
    iconURL(part.name).then((u) => { if (u) img.src = u; else img.replaceWith(el('span', { class: 'gl' }, '·')); });
    return img;
  }
  if (part.type === 'text') return el('span', { class: 'mono', style: `background:${part.color}` }, part.text);
  return el('span', { class: 'gl' }, part.text);
}

function iconEl({ primary, badge }) {
  if (!primary) return null;
  const box = el('span', { class: 'ico', 'aria-hidden': 'true' }, partEl(primary));
  if (badge) box.append(el('span', { class: 'badge' }, partEl(badge)));
  return box;
}

// ---- import -------------------------------------------------------------

let importData = null;

function reportList(title, rows, bad = false) {
  if (!rows || !rows.length) return null;
  return [el('h4', {}, `${title} (${rows.length})`),
    el('ul', {}, ...rows.map((r) => el('li', { class: bad ? 'bad' : '' }, typeof r === 'string' ? r : `${r.name}: ${r.reason}`)))];
}

function showReport(rep) {
  $('import-summary').textContent = rep.dryRun
    ? `Nothing is changed yet. This file would add ${rep.created.length} items and ${(rep.tagsCreated || []).length} tag settings.`
    : 'Done.';
  $('import-report').replaceChildren(...[
    reportList('New tags', rep.tagsCreated), reportList('Tags already here', rep.tagsReused),
    reportList('Items', (rep.created || []).map((a) => (rep.commands && rep.commands[a] ? `${a}: ${rep.commands[a]}` : a))), reportList('Icons', rep.icons),
    reportList('Skipped', rep.skipped), reportList('Rejected', rep.rejected, true), reportList('Icon problems', rep.iconProblems, true),
  ].flat().filter(Boolean));
}

$('import').addEventListener('click', () => { $('import-file').value = ''; $('import-file').click(); });
$('import-file').addEventListener('change', async (e) => {
  const file = e.target.files[0];
  if (!file) return;
  try {
    importData = JSON.parse((await file.text()).replace(/^﻿/, ''));
  } catch (err) {
    fail(new Error(`${file.name} is not JSON: ${err.message}`));
    return;
  }
  try {
    const rep = await api('POST', '/api/import', { dryRun: true, file: importData });
    showReport(rep);
    $('import-ok').disabled = !rep.created.length && !(rep.tagsCreated || []).length;
    $('import-dialog').returnValue = '';
    $('import-dialog').showModal();
  } catch (err) { fail(err); }
});
$('import-dialog').addEventListener('close', async () => {
  if ($('import-dialog').returnValue !== 'ok' || !importData) return;
  try {
    const rep = await api('POST', '/api/import', { dryRun: false, file: importData });
    toast(`Imported ${rep.created.length} items and ${(rep.tagsCreated || []).length} tag settings.`);
    if (rep.rejected.length || rep.iconProblems.length) { showReport(rep); $('import-ok').disabled = true; $('import-dialog').showModal(); }
  } catch (err) { fail(err); }
  importData = null;
  await refresh();
});

function stateText(item) {
  const s = item.status || {};
  return s.state === 'idle' || !s.state ? 'idle' : s.state;
}

function itemRow(item) {
  const st = stateText(item);
  const last = (item.status && item.status.last) || '';
  const busy = !!state.pending[item.id];
  const live = isActive(item);
  const variants = variantsOf(item);

  const runBtn = el('button', {
    type: 'button', title: 'Run', 'aria-label': `Run ${item.alias}`, disabled: busy || live,
    onclick: (e) => { e.stopPropagation(); act(item, 'run', { variant: '' }); },
  }, '▶', el('span', { class: 'txt' }, ' Run'));
  const split = el('span', { class: variants.length > 1 ? 'split has-arrow' : 'split' }, runBtn);
  if (variants.length > 1) {
    split.append(el('button', {
      type: 'button', title: 'Choose a command variant', 'aria-label': `Variants of ${item.alias}`, disabled: busy || live,
      onclick: (e) => {
        e.stopPropagation();
        showMenu(e.currentTarget, variants.map((v) => ({
          label: v.label && v.label !== v.id ? `${v.label}` : v.id, hint: v.id === 'default' ? 'default command' : v.id,
          run: () => act(item, 'run', { variant: v.id === 'default' ? '' : v.id }),
        })));
      },
    }, '▾'));
  }

  const actions = el('div', { class: 'actions' });
  if (item.kind === 'link') {
    actions.append(button('↗', `Open ${item.alias}`, async () => {
      try {
        await api('POST', `/api/items/${encodeURIComponent(item.id)}/open`);
        toast(`Opened ${item.link ? item.link.target : item.alias}.`);
      } catch (err) { fail(err); }
    }, busy, 'Open'));
  } else if (live) {
    const url = serviceURL(item);
    if (url) {
      actions.append(button('↗', `Open ${url}`, async () => {
        try { await api('POST', '/api/shell/open-url', { url }); } catch (err) { fail(err); }
      }, false, 'Open'));
    }
    actions.append(
      button('■', 'Stop', () => act(item, 'stop', {}), busy),
      button('↻', 'Restart', () => act(item, 'restart', {}), busy),
      button('⌨', 'Open in terminal', () => act(item, 'terminal'), busy, 'Terminal'),
    );
  } else {
    actions.append(split);
  }
  if (item.kind !== 'link') {
    actions.append(button('⧉', 'Copy command', (e) => copyMenu(e.currentTarget, item)));
  }
  actions.append(
    button(item.pinned ? '★' : '☆', item.pinned ? `Unpin ${item.alias} from Home` : `Pin ${item.alias} to Home`, () => setPinned(item, !item.pinned), busy, item.pinned ? 'Unpin' : 'Pin'),
    button('✎', 'Edit', () => openEditor(item)),
    button('✕', 'Delete', () => askDelete(item)),
  );

  const chips = el('div', { class: 'chips' }, kindChip(item));
  if (item.service && item.service.port) chips.append(el('span', { class: 'chip port', title: 'Port' }, `:${item.service.port}`));
  for (const t of item.tags || []) chips.append(tagChip(t));

  const picking = state.selectMode;
  const picked = state.picked.has(item.id);
  const open = () => (picking ? togglePick(item.id) : select(item.id));
  return el('div', {
    class: `row${state.selected === item.id ? ' selected' : ''}${picking ? ' with-pick' : ''}${picked ? ' picked' : ''}`, tabindex: '0', role: 'button',
    'aria-label': picking ? `${item.alias}: ${picked ? 'selected' : 'not selected'}` : `${item.alias}: details`,
    onclick: open,
    onkeydown: (e) => { if ((e.key === 'Enter' || (picking && e.key === ' ')) && e.target === e.currentTarget) { e.preventDefault(); open(); } },
  },
  picking ? el('input', {
    type: 'checkbox', class: 'pick', checked: picked, 'aria-label': `Select ${item.alias}`,
    onclick: (e) => { e.stopPropagation(); togglePick(item.id); },
  }) : null,
  el('span', { class: `dot ${st}${item.attention ? ' attn' : ''}`, title: item.attention ? `Needs you: ${item.attention}` : st }),
  el('div', { class: 'main' },
    iconEl(itemIcon(item, mainTagOf(item))),
    el('div', { class: 'text' }, el('span', { class: 'alias' }, item.alias), el('span', { class: 'title' }, item.title), chips)),
  el('div', { class: 'status' },
    item.attention ? el('span', { class: 'attn-label', title: item.attention }, `needs you · ${item.attention}`) : null,
    el('span', { class: `state ${st}` }, busy ? '…' : st),
    item.health ? el('span', { class: `health ${item.health}`, title: 'Health check of the service' }, item.health) : null,
    item.nextRun && formatNext(item.nextRun) ? el('span', { class: 'last next', title: `Runs by itself: ${scheduleSpec(item.task && item.task.schedule)}${item.task && item.task.missed === 'catchup' ? ' · missed runs: once when back' : ' · missed runs: skipped'}` }, `next: ${formatNext(item.nextRun)}${item.task && item.task.missed === 'catchup' ? ' · catch-up' : ''}`) : null,
    last ? el('span', { class: `last${/failed/.test(last) ? ' bad' : ''}`, title: 'Result of the last finished run' }, `last: ${last}`) : null),
  actions);
}

function button(glyph, label, onclick, disabled = false, short = '') {
  return el('button', {
    type: 'button', title: label, 'aria-label': label, disabled,
    onclick: (e) => { e.stopPropagation(); onclick(e); },
  }, glyph, el('span', { class: 'txt' }, ` ${short || label.split(' ')[0]}`));
}

// ---- actions ------------------------------------------------------------

async function setPinned(item, on) {
  try {
    await api('PATCH', `/api/items/${encodeURIComponent(item.id)}`, { pinned: on });
    toast(on ? `Pinned ${item.alias} to Home.` : `Unpinned ${item.alias}.`);
  } catch (err) { fail(err); }
  await refresh();
}

async function act(item, action, body) {
  state.pending[item.id] = true;
  render();
  try {
    if (action === 'terminal') {
      await api('POST', `/api/items/${encodeURIComponent(item.id)}/terminal`);
      toast(`Opened ${item.alias} in a terminal.`);
    } else {
      await api('POST', `/api/items/${encodeURIComponent(item.id)}/${action}`, body);
    }
  } catch (err) {
    fail(err);
  } finally {
    delete state.pending[item.id];
    await refresh();
  }
}

function copyMenu(anchor, item) {
  const variants = variantsOf(item);
  const entries = [
    { label: 'Copy command', hint: 'cd, environment and command', run: () => copyCommand(item, '', false) },
    { label: 'Copy command only', hint: 'just the command line', run: () => copyCommand(item, '', true) },
  ];
  for (const v of variants.slice(1)) {
    entries.push({ label: `Copy: ${v.label || v.id}`, hint: 'variant, full form', run: () => copyCommand(item, v.id, false) });
  }
  showMenu(anchor, entries);
}

async function copyCommand(item, variant, bare) {
  try {
    const q = new URLSearchParams();
    if (variant) q.set('variant', variant);
    if (bare) q.set('bare', '1');
    const { text } = await api('GET', `/api/items/${encodeURIComponent(item.id)}/command?${q}`);
    await copyText(text);
    toast(`Copied: ${text.length > 80 ? `${text.slice(0, 77)}…` : text}`);
  } catch (err) {
    fail(err);
  }
}

async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text);
    return;
  } catch { /* fall back below */ }
  const ta = el('textarea', { style: 'position:fixed;opacity:0' });
  ta.value = text;
  document.body.append(ta);
  ta.select();
  const ok = document.execCommand('copy');
  ta.remove();
  if (!ok) throw new Error('The clipboard is not available. Select the text in the details panel instead.');
}

function askDelete(item) {
  $('confirm-text').textContent = `Delete “${item.alias}”? Its history is removed too.`;
  const dlg = $('confirm');
  dlg.addEventListener('close', async () => {
    if (dlg.returnValue !== 'ok') return;
    try {
      await api('DELETE', `/api/items/${encodeURIComponent(item.id)}`);
      if (state.selected === item.id) closeDrawer();
      toast(`Deleted ${item.alias}.`);
    } catch (err) { fail(err); }
    await refresh();
  }, { once: true });
  dlg.returnValue = '';
  dlg.showModal();
}

// ---- menu ---------------------------------------------------------------

function showMenu(anchor, entries) {
  const menu = $('menu');
  // a modal dialog makes everything outside it unclickable, so the menu joins it
  const host = anchor.closest('dialog[open]') || document.body;
  if (menu.parentElement !== host) host.append(menu);
  menu.replaceChildren(...entries.map((e) => el('button', {
    type: 'button', role: 'menuitem',
    onclick: () => { hideMenu(); e.run(); },
  }, e.label, e.hint ? el('span', { class: 'hint' }, e.hint) : null)));
  menu.hidden = false;
  const r = anchor.getBoundingClientRect();
  const w = menu.offsetWidth;
  menu.style.left = `${Math.max(8, Math.min(window.innerWidth - w - 8, r.right - w))}px`;
  menu.style.top = `${Math.min(window.innerHeight - menu.offsetHeight - 8, r.bottom + 4)}px`;
  menu.querySelector('button')?.focus();
}

function hideMenu() { $('menu').hidden = true; }
document.addEventListener('click', (e) => { if (!$('menu').contains(e.target)) hideMenu(); }, true);

// ---- drawer -------------------------------------------------------------

function select(id) {
  state.selected = id;
  const it = itemById(id);
  if (it && it.attention) { // opening an item answers its call
    api('POST', `/api/attention/${encodeURIComponent(id)}/clear`).then(scheduleRefresh).catch(() => {});
  }
  render();
  loadDrawer();
}

function closeDrawer() {
  state.selected = null;
  $('drawer').hidden = true;
  render();
}

async function loadDrawer() {
  const item = itemById(state.selected);
  if (!item) { closeDrawer(); return; }
  const id = encodeURIComponent(item.id);
  const [cmd, hist] = await Promise.all([
    item.kind === 'link' ? Promise.resolve({ text: item.link ? item.link.target : '' }) : api('GET', `/api/items/${id}/command?bare=1`).catch((e) => ({ text: `(${e.message})` })),
    api('GET', `/api/items/${id}/history`).catch(() => []),
  ]);
  const latest = (item.status && item.status.runId) || (hist.length ? hist[hist.length - 1].runId : '');
  const log = latest ? await fetch(`/api/runs/${latest}/log?tail=40`, { headers: { 'X-OrchDock-Token': token } }).then((r) => (r.ok ? r.text() : '')).catch(() => '') : '';

  $('drawer-title').textContent = item.alias;
  const c = item.command || {};
  const rows = [
    ['Title', item.title], ['Kind', ((kindDef(item.kindId) || kindDef(item.kind)) || {}).name || item.kind], ['Tags', (item.tags || []).join(', ') || '—'], ['State', stateText(item)], ['Id', item.id],
    ['Folder', c.cwd || ((mainTagOf(item) || {}).path ? `${mainTagOf(item).path} (from tag ${mainTagOf(item).name})` : '(default)')], ['Pinned', item.pinned ? 'yes' : 'no'], ['Notes', item.notes || '—'],
  ];
  const dl = el('dl');
  for (const [k, v] of rows) dl.append(el('dt', {}, k), el('dd', {}, v));

  const table = el('table', {}, el('thead', {}, el('tr', {}, ...['Started', 'Result', 'Exit', 'Took', 'Trigger', ''].map((h) => el('th', {}, h)))));
  const body = el('tbody');
  for (const h of [...hist].reverse().slice(0, 15)) {
    body.append(el('tr', {},
      el('td', {}, h.startedAt ? new Date(h.startedAt).toLocaleString() : '—'),
      el('td', { class: h.outcome === 'ok' ? 'ok' : /failed/.test(h.outcome) ? 'bad' : '' }, h.outcome),
      el('td', {}, h.exitCode == null ? '—' : h.exitCode),
      el('td', {}, h.durationMs ? `${(h.durationMs / 1000).toFixed(1)}s` : '—'),
      el('td', {}, `${h.trigger}${h.variant && h.variant !== 'default' ? ` / ${h.variant}` : ''}${h.count > 1 ? ` (${h.count} missed)` : ''}`),
      el('td', {}, h.runId && h.log ? el('button', { type: 'button', class: 'link', 'aria-label': `Log of the run started ${h.startedAt}`, onclick: () => showRunLog(h) }, 'log') : '')));
  }
  table.append(body);

  $('drawer-body').replaceChildren(
    dl,
    el('h3', {}, item.kind === 'link' ? 'Target' : 'Command'),
    el('pre', {}, cmd.text || '—'),
    el('h3', {}, 'Recent runs'),
    hist.length ? table : el('p', { class: 'summary' }, 'No runs yet.'),
    el('h3', { id: 'log-title' }, 'Latest output'),
    el('pre', { id: 'log-text' }, log.trim() || '—'),
  );
  $('drawer').hidden = false;
}

$('drawer-close').addEventListener('click', closeDrawer);

// showRunLog puts one run's output in the drawer.
async function showRunLog(h) {
  const text = await fetch(`/api/runs/${encodeURIComponent(h.runId)}/log?tail=400`, { headers: { 'X-OrchDock-Token': token } })
    .then((r) => (r.ok ? r.text() : `(${r.status} ${r.statusText})`)).catch((e) => `(${e.message})`);
  $('log-title').textContent = `Output of the run started ${h.startedAt ? new Date(h.startedAt).toLocaleString() : ''} (${h.outcome})`;
  $('log-text').textContent = text.trim() || '—';
  $('log-text').scrollIntoView({ block: 'nearest' });
}

// ---- editor -------------------------------------------------------------

const SHELLS = ['auto', 'pwsh', 'powershell', 'cmd', 'bash', 'zsh', 'sh', 'none'];

function syncEditorFields() {
  const kind = baseOf($('f-kind').value);
  const link = kind === 'link';
  for (const id of ['l-shell', 'l-command', 'l-cwd', 'l-env', 'l-keep']) $(id).hidden = link;
  $('l-target').hidden = !link;
  $('l-service').hidden = kind !== 'service';
  $('l-schedule').hidden = kind !== 'task';
  $('schedule-preview').hidden = kind !== 'task';
  const none = $('f-shell').value === 'none';
  $('t-command').textContent = none ? 'Program and arguments (one per line)' : kind === 'shell' ? 'Command (empty: an interactive shell)' : 'Command line';
  $('f-command').required = !link && kind !== 'shell';
}

function openEditor(item) {
  state.editing = item || null;
  $('editor-title').textContent = item ? `Edit ${item.alias}` : 'New item';
  $('editor-error').hidden = true;
  const c = (item && item.command) || {};
  fillKinds(item);
  $('f-title').value = item ? item.title : '';
  $('f-alias').value = item ? item.alias : '';
  $('f-shell').value = SHELLS.includes(c.shell) ? c.shell : 'auto';
  $('f-command').value = c.argv && c.argv.length ? c.argv.join('\n') : c.line || '';
  $('f-cwd').value = c.cwd || '';
  $('f-target').value = item && item.link ? item.link.target : '';
  state.editIcon = item && item.icon && (item.icon.primary || (item.icon.badges || []).length) ? item.icon : null;
  $('f-tags').value = ((item && item.tags) || []).join(', ');
  $('f-pinned').checked = !!(item && item.pinned);
  suggestTags();
  $('f-env').value = envToText(c.env);
  $('f-keep').checked = !!(item && item.interactive && item.interactive.keepOpenOnExit);
  const svc = (item && item.service) || {};
  $('f-port').value = svc.port || '';
  $('f-restart').value = svc.restart || 'never';
  const tk = (item && item.task) || {};
  $('f-schedule').value = scheduleSpec(tk.schedule);
  $('f-missed').value = tk.missed || 'skip';
  previewSchedule();
  syncEditorFields();
  drawEditorIcon();
  $('editor').showModal();
  $('f-title').focus();
}

$('f-kind').addEventListener('change', () => { syncEditorFields(); if (!state.editing) state.editIcon = null; drawEditorIcon(); });

// live preview of a typed schedule
let previewTimer = 0;
function previewSchedule() {
  clearTimeout(previewTimer);
  const spec = $('f-schedule').value.trim();
  const out = $('schedule-preview');
  if (!spec) { out.textContent = 'Leave empty to run only when you start it.'; out.className = 'hint-text'; return; }
  previewTimer = setTimeout(async () => {
    try {
      const r = await api('GET', `/api/schedule/preview?spec=${encodeURIComponent(spec)}`);
      out.textContent = `Next runs: ${(r.next || []).map((t) => formatNext(t)).join(' · ')}`;
      out.className = 'hint-text';
    } catch (err) {
      out.textContent = err.message;
      out.className = 'error';
    }
  }, 250);
}
$('f-schedule').addEventListener('input', previewSchedule);
$('f-shell').addEventListener('change', syncEditorFields);
$('new').addEventListener('click', () => openEditor(null));
$('editor-cancel').addEventListener('click', () => $('editor').close());

$('editor-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const err = $('editor-error');
  err.hidden = true;
  const kindRef = $('f-kind').value;
  const kind = baseOf(kindRef);
  const { env, error } = parseEnvText($('f-env').value);
  if (error) { err.textContent = `Environment: ${error}`; err.hidden = false; return; }
  const body = { title: $('f-title').value.trim(), tags: parseTags($('f-tags').value), kindId: kindRef.startsWith('k_') ? kindRef : '' };
  const alias = $('f-alias').value.trim();
  if (alias || state.editing) body.alias = alias || state.editing.alias;
  body.pinned = $('f-pinned').checked;
  if (state.editIcon) body.icon = state.editIcon;
  if (kind === 'link') {
    body.link = { target: $('f-target').value.trim() };
  } else {
    const shell = $('f-shell').value;
    const text = $('f-command').value;
    body.command = {
      shell, cwd: $('f-cwd').value.trim(), env,
      line: shell === 'none' ? '' : text.trim(),
      argv: shell === 'none' ? text.split(/\r?\n/).map((s) => s.trim()).filter(Boolean) : null,
    };
    body.interactive = { ...(state.editing ? state.editing.interactive : {}), keepOpenOnExit: $('f-keep').checked };
  }
  if (kind === 'task') {
    body.task = { ...((state.editing && state.editing.task) || {}), missed: $('f-missed').value };
    body.scheduleSpec = $('f-schedule').value.trim();
  }
  if (kind === 'service') {
    const port = parseInt($('f-port').value, 10);
    body.service = {
      ...((state.editing && state.editing.service) || {}),
      port: port > 0 ? port : 0,
      portMode: port > 0 ? 'fixed' : 'auto',
      restart: $('f-restart').value,
    };
  }
  try {
    if (state.editing) {
      await api('PATCH', `/api/items/${encodeURIComponent(state.editing.id)}`, body);
    } else {
      await api('POST', '/api/items', { kind, ...body });
    }
    const firstSchedule = kind === 'task' && body.scheduleSpec && !state.items.some((i) => i.task && i.task.schedule && i.task.schedule.enabled);
    $('editor').close();
    toast(state.editing ? 'Saved.' : 'Item added.');
    if (firstSchedule) offerAutostart();
    await refresh();
  } catch (ex) {
    err.textContent = ex.message;
    err.hidden = false;
  }
});

// Schedules run only while OrchDock runs: offer to start it at sign-in once.
async function offerAutostart() {
  try {
    const a = await api('GET', '/api/autostart');
    if (!a.supported || a.enabled) return;
    if (!window.confirm('Scheduled tasks run only while OrchDock is running. Start OrchDock in the tray when you sign in?')) return;
    await api('PUT', '/api/autostart', { enabled: true });
    toast('OrchDock will start in the tray when you sign in.');
  } catch (err) { fail(err); }
}

// ---- ports --------------------------------------------------------------

async function renderPorts() {
  const box = $('ports-list');
  try {
    const rows = await api('GET', '/api/ports');
    if (!rows.length) { box.replaceChildren(el('p', { class: 'hint-text' }, 'No service has a port yet.')); return; }
    box.replaceChildren(el('table', { class: 'ports' },
      el('thead', {}, el('tr', {}, ...['Port', 'Item', 'Now'].map((h) => el('th', {}, h)))),
      el('tbody', {}, ...rows.map((r) => {
        let now = 'free';
        let cls = '';
        if (r.running) { now = r.listening ? 'served by this item' : 'running, not answering yet'; cls = 'ok'; }
        if (r.holder) { now = `in use by ${r.holder}`; cls = 'bad'; }
        return el('tr', {},
          el('td', { class: 'mono' }, r.port),
          el('td', {}, r.alias),
          el('td', { class: cls }, now, r.conflict ? el('span', { class: 'bad' }, ' · shared with another item') : null));
      }))));
  } catch (err) { box.replaceChildren(el('p', { class: 'error' }, err.message)); }
}

$('ports').addEventListener('click', () => { renderPorts(); $('ports-dialog').showModal(); });
$('ports-refresh').addEventListener('click', renderPorts);

// ---- kinds --------------------------------------------------------------

const KIND_HELP = {
  task: 'a command that finishes', service: 'keeps running', agent: 'an AI coding session',
  shell: 'interactive', link: 'folder or address',
};
const kindDef = (id) => state.kinds.find((k) => k.id === id);
const baseOf = (id) => (kindDef(id) || { base: id }).base || id;

function kindChip(item) {
  const k = kindDef(item.kindId) || kindDef(item.kind);
  const name = (k && k.name) || item.kind;
  const color = k && k.color;
  return el('span', { class: color ? 'chip kind colored' : 'chip kind', style: color ? `background:${color}` : null, title: k && k.base !== name ? `behaves like a ${k.base}` : null }, name);
}

/** Kind choices for the editor: all kinds for a new item; for an existing one, the kinds with its behaviour. */
function fillKinds(item) {
  const sel = $('f-kind');
  const list = state.kinds.length ? state.kinds : Object.keys(KIND_HELP).map((id) => ({ id, name: id, base: id }));
  const order = ['task', 'service', 'agent', 'shell', 'link'];
  const sorted = [...list].sort((a, b) => order.indexOf(a.base) - order.indexOf(b.base) || (a.id.startsWith('k_') ? 1 : 0) - (b.id.startsWith('k_') ? 1 : 0));
  sel.replaceChildren(...sorted.filter((k) => !item || k.base === item.kind).map((k) => el('option', { value: k.id },
    k.id.startsWith('k_') ? `${k.name} (behaves like a ${k.base})` : `${k.name[0].toUpperCase()}${k.name.slice(1)} (${KIND_HELP[k.base]})`)));
  sel.value = item ? (item.kindId || item.kind) : 'task';
  sel.disabled = !!item && sel.options.length < 2;
}

function kindsError(message) {
  const e = $('kinds-error');
  e.textContent = message || '';
  e.hidden = !message;
}

function renderKinds() {
  const count = (k) => state.items.filter((i) => (k.id.startsWith('k_') ? i.kindId === k.id : i.kind === k.id && !i.kindId)).length;
  const patch = async (k, body) => {
    try {
      await api('PATCH', `/api/kinds/${encodeURIComponent(k.id)}`, body);
      kindsError('');
      await refresh();
    } catch (err) { kindsError(err.message); }
  };
  $('kinds-list').replaceChildren(...state.kinds.map((k) => {
    const color = el('input', { type: 'color', value: k.color || '#64748b', 'aria-label': `Colour of ${k.name}` });
    color.addEventListener('change', () => patch(k, { color: color.value }));
    const name = el('input', { value: k.name, 'aria-label': `Name of ${k.name}`, autocomplete: 'off' });
    name.addEventListener('change', () => patch(k, { name: name.value.trim() }));
    const n = count(k);
    const remove = async (moveTo) => {
      try {
        await api('DELETE', `/api/kinds/${encodeURIComponent(k.id)}${moveTo ? `?moveTo=${encodeURIComponent(moveTo)}` : ''}`);
        kindsError('');
        await refresh();
      } catch (err) { kindsError(err.message); }
    };
    const own = k.id.startsWith('k_');
    const move = (by) => async () => {
      try {
        await api('POST', `/api/kinds/${encodeURIComponent(k.id)}/move`, { by });
        await refresh();
      } catch (err) { kindsError(err.message); }
    };
    const del = own
      ? el('span', { class: 'kbtns' },
        el('button', { type: 'button', 'aria-label': `Move ${k.name} up`, title: 'Move up', onclick: move(-1) }, '↑'),
        el('button', { type: 'button', 'aria-label': `Move ${k.name} down`, title: 'Move down', onclick: move(1) }, '↓'),
        el('button', {
          type: 'button', 'aria-label': `Delete ${k.name}`, title: `Delete ${k.name}`,
          onclick: (e) => {
            if (!n) { remove(''); return; }
            // ask where its items go: the plain base kind or another kind with the same behaviour
            const targets = state.kinds.filter((x) => x.base === k.base && x.id !== k.id && (x.id === k.base || x.id.startsWith('k_')));
            showMenu(e.currentTarget, targets.map((x) => ({
              label: `Move ${n} item${n > 1 ? 's' : ''} to “${x.name}”`, hint: x.id === k.base ? 'the plain built-in kind' : 'then delete', run: () => remove(x.id),
            })));
          },
        }, '✕'))
      : el('span', { class: 'base', title: 'Built-in kinds cannot be deleted' }, 'built-in');
    return el('div', { class: 'krow' }, color, name,
      el('span', { class: 'base' }, own ? `like a ${k.base}` : ''), el('span', { class: 'count' }, `${n} items`), del);
  }));
}

$('kinds').addEventListener('click', () => {
  kindsError('');
  renderKinds();
  $('kinds-dialog').showModal();
  $('k-name').focus();
});

$('k-add').addEventListener('click', async () => {
  const name = $('k-name').value.trim();
  if (!name) { kindsError('Give the kind a name.'); return; }
  try {
    await api('POST', '/api/kinds', { name, base: $('k-base').value, color: $('k-color').value });
    $('k-name').value = '';
    kindsError('');
    await refresh();
  } catch (err) { kindsError(err.message); }
});

$('k-name').addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); $('k-add').click(); } });

// ---- icon picker ------------------------------------------------------------

const DEFAULT_ICONS = {
  agent: { primary: 'project', badges: ['agent'] }, service: { primary: 'project', badges: ['builtin:server'] },
  task: { primary: 'project', badges: ['builtin:play'] }, shell: { primary: 'project', badges: ['builtin:terminal'] },
  link: { primary: 'builtin:link', badges: [] },
};

/**
 * openIconPicker lets the user choose an icon. ctx: {icon, forTag, preview(icon) -> {primary, badge}}.
 * Resolves with the chosen icon, or null when cancelled.
 */
function openIconPicker(ctx) {
  const dlg = $('icon-dialog');
  const work = { primary: (ctx.icon && ctx.icon.primary) || '', badges: [...((ctx.icon && ctx.icon.badges) || [])] };
  let target = 'primary';
  let tab = 'builtin';
  const err = (m) => { $('ip-error').textContent = m || ''; $('ip-error').hidden = !m; };
  const current = () => (target === 'primary' ? work.primary : work.badges[0] || '');
  const set = (ref) => {
    if (target === 'primary') work.primary = ref;
    else work.badges = ref ? [ref] : [];
    err('');
    draw();
  };
  const tabs = [['builtin', 'Built-in'], ['emoji', 'Emoji'], ['upload', 'Upload'], ['monogram', 'Monogram'],
    ['auto', ctx.forTag ? 'From folder' : 'Automatic'], ['none', 'None']];

  function choice(ref, label, active) {
    const part = iconPart(ref);
    return el('button', { type: 'button', class: active ? 'on' : '', title: label, 'aria-label': label, onclick: () => set(ref) },
      part ? iconEl({ primary: part }) : label);
  }

  function panel() {
    const box = $('ip-panel');
    const cur = current();
    if (tab === 'builtin') {
      box.replaceChildren(el('div', { class: 'ip-grid' }, ...BUILTIN_ICONS.map((n) => choice(`builtin:${n}`, n, cur === `builtin:${n}`))));
    } else if (tab === 'emoji') {
      const inp = el('input', { placeholder: 'type or paste one emoji', value: cur.startsWith('emoji:') ? cur.slice(6) : '', 'aria-label': 'Emoji' });
      box.replaceChildren(el('div', { class: 'ip-row' }, inp,
        el('button', { type: 'button', onclick: () => { const v = inp.value.trim(); if (v) set(`emoji:${v}`); } }, 'Use emoji')));
    } else if (tab === 'monogram') {
      const m = /^text:([^:]{1,8}):(#[0-9a-fA-F]{6})$/.exec(cur) || [];
      const txt = el('input', { maxlength: '2', placeholder: 'AB', value: m[1] || '', 'aria-label': 'Letters' });
      const col = el('input', { type: 'color', value: m[2] || '#4f8cff', 'aria-label': 'Colour' });
      box.replaceChildren(el('div', { class: 'ip-row' }, txt, col,
        el('button', {
          type: 'button',
          onclick: () => { const v = txt.value.trim(); if (v) set(`text:${v}:${col.value}`); else err('Type one or two letters.'); },
        }, 'Use letters')));
    } else if (tab === 'upload') {
      const file = el('input', { type: 'file', accept: '.png,.jpg,.jpeg,.ico,.svg', 'aria-label': 'Image file' });
      file.addEventListener('change', async () => {
        const f = file.files[0];
        if (!f) return;
        if (f.size > 512 * 1024) { err('An icon must be 512 KB or smaller.'); return; }
        try {
          const data = await new Promise((res, rej) => {
            const r = new FileReader();
            r.onload = () => res(String(r.result).split(',')[1] || '');
            r.onerror = rej;
            r.readAsDataURL(f);
          });
          const r = await api('POST', '/api/icons', { name: f.name, data });
          set(r.ref);
        } catch (e) { err(e.message); }
      });
      const grid = el('div', { class: 'ip-grid' });
      box.replaceChildren(el('div', { class: 'ip-row' }, file, el('span', { class: 'hint-text' }, 'png, jpg, ico or svg, up to 512 KB')), grid);
      api('GET', '/api/icons').then((r) => grid.replaceChildren(...(r.icons || []).map((n) => choice(`file:${n}`, n, cur === `file:${n}`)))).catch(() => {});
    } else if (tab === 'auto') {
      const opts = ctx.forTag
        ? [['auto', 'Find an icon in the tag folder (favicon, logo...), or use its initials']]
        : [['project', 'The icon of its main tag (the first tag with a folder)'], ['agent', 'The agent logo (from its command)'], ['favicon', 'The service web icon']];
      box.replaceChildren(...opts.map(([ref, label]) => el('div', { class: 'ip-row' },
        el('button', { type: 'button', class: cur === ref ? 'primary' : '', onclick: () => set(ref) }, label))));
    } else {
      box.replaceChildren(el('div', { class: 'ip-row' }, el('button', { type: 'button', onclick: () => set('') },
        target === 'primary' ? 'No main icon' : 'No badge')));
    }
  }

  function draw() {
    $('ip-preview').replaceChildren(iconEl(ctx.preview(work)) || el('span', { class: 'hint-text' }, 'no icon'));
    for (const b of $('ip-target').querySelectorAll('button')) b.setAttribute('aria-checked', String(b.dataset.target === target));
    $('ip-target').hidden = !!ctx.forTag; // tags have no badges
    $('ip-tabs').replaceChildren(...tabs.map(([id, label]) => el('button', {
      type: 'button', role: 'tab', 'aria-selected': String(id === tab), onclick: () => { tab = id; draw(); },
    }, label)));
    panel();
  }

  for (const b of $('ip-target').querySelectorAll('button')) b.onclick = () => { target = b.dataset.target; draw(); };
  err('');
  draw();
  dlg.returnValue = '';
  dlg.showModal();
  return new Promise((resolve) => {
    dlg.addEventListener('close', () => resolve(dlg.returnValue === 'ok' ? { primary: work.primary, badges: work.badges } : null), { once: true });
  });
}

function editorMainTag() {
  return mainTag({ tags: parseTags($('f-tags').value) }, state.tags);
}

// suggested tags: the tags whose folder holds the working folder; never added by themselves
let suggestTimer = 0;
function suggestTags() {
  clearTimeout(suggestTimer);
  const box = $('tag-suggest');
  const cwd = $('f-cwd').value.trim();
  if (!cwd) { box.replaceChildren(); return; }
  suggestTimer = setTimeout(async () => {
    try {
      const { tags } = await api('GET', `/api/tags/suggest?cwd=${encodeURIComponent(cwd)}`);
      const have = parseTags($('f-tags').value);
      const left = tags.filter((t) => !have.includes(t));
      box.replaceChildren(...(left.length ? [el('span', { class: 'hint-text' }, 'Suggested by the folder:'), ...left.map((t) => el('button', {
        type: 'button', class: 'chip sugg', title: `Add the tag ${t}`, onclick: () => {
          $('f-tags').value = [...parseTags($('f-tags').value), t].join(', ');
          suggestTags();
          drawEditorIcon();
        },
      }, `+ ${t}`))] : []));
    } catch { box.replaceChildren(); }
  }, 200);
}
$('f-cwd').addEventListener('input', suggestTags);
$('f-tags').addEventListener('input', () => { suggestTags(); drawEditorIcon(); });

function drawEditorIcon() {
  const kind = baseOf($('f-kind').value);
  const item = { ...(state.editing || {}), kind, icon: state.editIcon || DEFAULT_ICONS[kind], command: { line: $('f-command').value } };
  $('f-icon').replaceChildren(iconEl(itemIcon(item, editorMainTag())) || '-');
}

$('f-icon').addEventListener('click', async () => {
  const kind = baseOf($('f-kind').value);
  const base = { ...(state.editing || {}), kind, command: { line: $('f-command').value } };
  const icon = await openIconPicker({
    icon: state.editIcon || DEFAULT_ICONS[kind],
    preview: (ic) => itemIcon({ ...base, icon: ic }, editorMainTag()),
  });
  if (icon) { state.editIcon = icon; drawEditorIcon(); }
});

// ---- manage menu ----------------------------------------------------------

$('manage').addEventListener('click', (e) => {
  e.stopPropagation();
  const p = $('manage-panel');
  p.hidden = !p.hidden;
  $('manage').setAttribute('aria-expanded', String(!p.hidden));
});
$('manage-panel').addEventListener('click', () => { $('manage-panel').hidden = true; $('manage').setAttribute('aria-expanded', 'false'); });
document.addEventListener('click', (e) => {
  if (!$('manage-panel').hidden && !e.target.closest('.manage-wrap')) $('manage-panel').hidden = true;
});

// ---- sections (saved searches shown on Home) ----------------------------------

function renderViews() {
  const box = $('views');
  const chips = state.views.map((v) => el('button', {
    type: 'button', class: `vchip${state.query === v.query ? ' on' : ''}`, title: `${v.query} — show in All items`, onclick: () => applySection(v),
  }, v.name));
  if (state.query.trim() && !state.views.some((v) => v.query.trim() === state.query.trim())) {
    chips.push(el('button', {
      type: 'button', class: 'save', onclick: () => {
        const name = window.prompt('Name this section (it shows on Home)', state.query.trim().replace(/^tag:/, ''));
        if (name && name.trim()) addSection(name.trim(), state.query.trim());
      },
    }, '☆ Save this search as a section'));
  }
  box.replaceChildren(...chips);
}

/** Shows a section's items in All items. */
function applySection(v) {
  state.query = v.query;
  $('search').value = v.query;
  if (v.group && ['tag', 'kind', 'none'].includes(v.group)) { state.groupBy = v.group; $('group-by').value = v.group; prefs.set('groupBy', v.group); }
  setMode('all');
}

async function addSection(name, query) {
  try {
    await api('POST', '/api/views', { name, query });
    toast(`Added the section ${name} to Home.`);
    await refresh();
  } catch (err) { fail(err); }
}

async function renameSection(v) {
  const name = window.prompt('Section name', v.name);
  if (!name || !name.trim() || name.trim() === v.name) return;
  try { await api('PATCH', `/api/views/${encodeURIComponent(v.id)}`, { name: name.trim() }); await refresh(); } catch (err) { fail(err); }
}

async function moveSection(v, by) {
  try { await api('POST', `/api/views/${encodeURIComponent(v.id)}/move`, { by }); await refresh(); } catch (err) { fail(err); }
}

async function removeSection(v) {
  try {
    await api('DELETE', `/api/views/${encodeURIComponent(v.id)}`);
    toast(`Removed the section ${v.name}; its items are unchanged.`);
    await refresh();
  } catch (err) { fail(err); }
}

function sectionsError(message) {
  const e = $('sections-error');
  e.textContent = message || '';
  e.hidden = !message;
}

function renderSections() {
  const patch = async (v, body) => {
    try { await api('PATCH', `/api/views/${encodeURIComponent(v.id)}`, body); sectionsError(''); await refresh(); } catch (err) { sectionsError(err.message); }
  };
  $('sections-list').replaceChildren(...(state.views.length ? state.views.map((v, i) => {
    const name = el('input', { value: v.name, 'aria-label': `Name of the section ${v.name}`, autocomplete: 'off' });
    name.addEventListener('change', () => patch(v, { name: name.value.trim() }));
    const query = el('input', { value: v.query, 'aria-label': `Search of the section ${v.name}`, autocomplete: 'off', spellcheck: 'false' });
    query.addEventListener('change', () => patch(v, { query: query.value.trim() }));
    return el('div', { class: 'srow' }, name, query,
      el('span', { class: 'kbtns' },
        el('button', { type: 'button', disabled: i === 0, 'aria-label': `Move ${v.name} up`, title: 'Move up', onclick: () => moveSection(v, -1) }, '↑'),
        el('button', { type: 'button', disabled: i === state.views.length - 1, 'aria-label': `Move ${v.name} down`, title: 'Move down', onclick: () => moveSection(v, 1) }, '↓'),
        el('button', { type: 'button', 'aria-label': `Remove the section ${v.name}`, title: 'Remove (items stay)', onclick: () => removeSection(v) }, '✕')));
  }) : [el('p', { class: 'hint-text' }, 'No sections yet.')]));
}

$('sections').addEventListener('click', () => {
  sectionsError('');
  renderSections();
  $('sections-dialog').showModal();
  $('s-name').focus();
});

$('s-add').addEventListener('click', async () => {
  const name = $('s-name').value.trim();
  const query = $('s-query').value.trim();
  if (!name || !query) { sectionsError('Give the section a name and a search, for example tag:shop.'); return; }
  try {
    await api('POST', '/api/views', { name, query });
    $('s-name').value = '';
    $('s-query').value = '';
    sectionsError('');
    await refresh();
  } catch (err) { sectionsError(err.message); }
});

// ---- tags -------------------------------------------------------------------

function tagChip(t) {
  const d = tagDef(t);
  const c = d && d.color;
  return el('span', { class: c ? 'chip tagged' : 'chip', style: c ? `background:${c}` : null, title: d && d.path ? `folder: ${d.path}` : null }, `#${t}`);
}

function tagsError(message) {
  const e = $('tags-error');
  e.textContent = message || '';
  e.hidden = !message;
}

let tagFocus = '';

/** Every tag in use or defined: its icon, colour, name (rename), folder, item count and delete. */
function renderTags() {
  const count = {};
  for (const it of state.items) for (const t of it.tags || []) count[t] = (count[t] || 0) + 1;
  const names = [...state.tags.map((d) => d.name), ...Object.keys(count).filter((t) => !tagDef(t)).sort()];
  const put = async (t, body) => {
    try {
      await api('PUT', `/api/tags/${encodeURIComponent(t)}`, body);
      tagsError('');
      await refresh();
    } catch (err) { tagsError(err.message); }
  };
  $('tags-list').replaceChildren(...(names.length ? names.map((t) => {
    const d = tagDef(t) || { name: t };
    const n = count[t] || 0;
    const pick = el('button', {
      type: 'button', class: 'picon', title: `Icon of ${t}`, 'aria-label': `Icon of ${t}`,
      onclick: async () => {
        const icon = await openIconPicker({ forTag: true, icon: d.icon, preview: (ic) => ({ primary: tagIcon({ ...d, icon: ic }) }) });
        if (icon) put(t, { icon: { primary: icon.primary } });
      },
    }, iconEl({ primary: tagIcon(d) }));
    const color = el('input', { type: 'color', value: d.color || '#64748b', title: `Colour of ${t}`, 'aria-label': `Colour of ${t}` });
    color.addEventListener('change', () => put(t, { color: color.value }));
    const name = el('input', { value: t, 'aria-label': `Name of the tag ${t}`, autocomplete: 'off' });
    name.addEventListener('change', () => {
      const to = name.value.trim().toLowerCase().replace(/^#/, '');
      if (to && to !== t) put(t, { name: to });
    });
    const path = el('input', { value: d.path || '', placeholder: 'folder (optional)', 'aria-label': `Folder of ${t}`, autocomplete: 'off' });
    path.addEventListener('change', () => put(t, { path: path.value.trim() }));
    const del = el('button', {
      type: 'button', title: `Delete ${t}`, 'aria-label': `Delete the tag ${t}`,
      onclick: (e) => {
        const remove = async (fromItems) => {
          try {
            await api('DELETE', `/api/tags/${encodeURIComponent(t)}${fromItems ? '?fromItems=1' : ''}`);
            tagsError('');
            await refresh();
          } catch (err) { tagsError(err.message); }
        };
        const entries = [];
        if (tagDef(t)) entries.push({ label: 'Forget its settings', hint: 'folder, colour and icon; items keep the tag', run: () => remove(false) });
        if (n) entries.push({ label: `Remove it from ${n} item${n > 1 ? 's' : ''} too`, hint: 'the items stay', run: () => remove(true) });
        showMenu(e.currentTarget, entries);
      },
    }, '✕');
    const row = el('div', { class: `prow${tagFocus === t ? ' focus' : ''}`, 'data-tag': t }, pick, color, name, path, el('span', { class: 'count' }, `${n} items`), del);
    return row;
  }) : [el('p', { class: 'hint-text' }, 'No tags yet. Tag items in their editor, or add one below.')]));
  if (tagFocus) {
    const row = $('tags-list').querySelector(`[data-tag="${CSS.escape(tagFocus)}"]`);
    if (row) row.scrollIntoView({ block: 'nearest' });
  }
}

function openTags(focus = '') {
  tagFocus = focus;
  tagsError('');
  renderTags();
  $('tags-dialog').showModal();
  if (!focus) $('t-name').focus();
}
$('tags').addEventListener('click', () => openTags());
$('tags-dialog').addEventListener('close', () => { tagFocus = ''; });

$('t-add').addEventListener('click', async () => {
  const name = $('t-name').value.trim().toLowerCase().replace(/^#/, '');
  if (!name) { tagsError('Give the tag a name.'); return; }
  try {
    await api('PUT', `/api/tags/${encodeURIComponent(name)}`, { path: $('t-path').value.trim(), color: $('t-color').value });
    $('t-name').value = '';
    $('t-path').value = '';
    tagsError('');
    await refresh();
  } catch (err) { tagsError(err.message); }
});
$('t-name').addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); $('t-add').click(); } });
$('t-pick').addEventListener('click', async () => {
  try {
    const { path } = await api('POST', '/api/shell/pick-folder', { title: 'Tag folder' });
    if (path) $('t-path').value = path;
  } catch (err) { tagsError(err.status === 501 ? 'Type the folder path; this window cannot open a folder picker.' : err.message); }
});

// ---- trash ------------------------------------------------------------------

async function renderTrash() {
  try {
    const rows = await api('GET', '/api/trash');
    $('trash-empty').disabled = !rows.length;
    $('trash-list').replaceChildren(...(rows.length ? rows.map((r) => el('div', { class: 'trow' },
      el('span', {}, el('strong', {}, r.alias), ` ${r.title}`),
      el('span', { class: 'when' }, `deleted ${new Date(r.deletedAt).toLocaleDateString()}`),
      el('button', {
        type: 'button', onclick: async () => {
          try { const it = await api('POST', `/api/trash/${encodeURIComponent(r.id)}/restore`); toast(`Restored ${it.alias}.`); await refresh(); renderTrash(); } catch (err) { fail(err); }
        },
      }, 'Restore'),
      el('button', {
        type: 'button', 'aria-label': `Delete ${r.alias} for good`, onclick: async () => {
          if (!window.confirm(`Delete ${r.alias} and its history for good?`)) return;
          try { await api('DELETE', `/api/trash/${encodeURIComponent(r.id)}`); renderTrash(); } catch (err) { fail(err); }
        },
      }, '✕'))) : [el('p', { class: 'hint-text' }, 'The trash is empty.')]));
  } catch (err) { fail(err); }
}
$('trash').addEventListener('click', () => { renderTrash(); $('trash-dialog').showModal(); });
$('trash-empty').addEventListener('click', async () => {
  if (!window.confirm('Delete everything in the trash for good?')) return;
  try { await api('DELETE', '/api/trash'); renderTrash(); await refresh(); } catch (err) { fail(err); }
});

// ---- export -----------------------------------------------------------------

$('export').addEventListener('click', async () => {
  const withEnv = window.confirm('Include the values of environment variables?\n\nThey often hold secrets. Choose Cancel to leave them out (their names are kept).');
  try {
    const r = await fetch(`/api/export${withEnv ? '?includeEnv=1' : ''}`, { headers: { 'X-OrchDock-Token': token } });
    if (!r.ok) throw new Error(`${r.status} ${r.statusText}`);
    const url = URL.createObjectURL(await r.blob());
    const a = el('a', { href: url, download: `orchdock-export-${new Date().toISOString().slice(0, 10)}.json` });
    document.body.append(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 5000);
    toast(withEnv ? 'Exported, with environment values.' : 'Exported, without environment values.');
  } catch (err) { fail(err); }
});

// ---- bulk select ----------------------------------------------------------------

function setSelectMode(on) {
  state.selectMode = on;
  if (!on) state.picked.clear();
  $('select-mode').setAttribute('aria-pressed', String(on));
  $('select-mode').textContent = on ? 'Done' : 'Select';
  renderBulk();
  render();
}

function togglePick(id) {
  if (state.picked.has(id)) state.picked.delete(id);
  else state.picked.add(id);
  renderBulk();
  render();
}

function renderBulk() {
  const n = state.picked.size;
  $('bulk').hidden = !state.selectMode;
  $('bulk-count').textContent = n ? `${n} selected` : 'Pick items';
  for (const b of $('bulk').querySelectorAll('[data-bulk]')) b.disabled = !n;
}

$('select-mode').addEventListener('click', () => setSelectMode(!state.selectMode));
$('bulk-none').addEventListener('click', () => { state.picked.clear(); renderBulk(); render(); });
for (const b of $('bulk').querySelectorAll('[data-bulk]')) {
  b.addEventListener('click', async () => {
    const action = b.dataset.bulk;
    const refs = [...state.picked];
    let tag = '';
    if (action === 'tag' || action === 'untag') {
      tag = (window.prompt(action === 'tag' ? 'Tag to add' : 'Tag to remove') || '').trim().toLowerCase().replace(/^#/, '');
      if (!tag) return;
    }
    if (action === 'delete' && !window.confirm(`Move ${refs.length} items to the trash?`)) return;
    try {
      const { results } = await api('POST', '/api/bulk', { action, refs, tag });
      const bad = results.filter((r) => !r.ok);
      if (bad.length) toast(`${bad.length} of ${results.length} failed: ${bad[0].error}`, 'error');
      else toast(`Done for ${results.length} items.`);
      if (action === 'delete') state.picked.clear();
    } catch (err) { fail(err); }
    renderBulk();
    await refresh();
  });
}

// ---- command palette (Ctrl+K) -------------------------------------------------------

let paletteIndex = 0;
function paletteEntries(q) {
  const word = q.trim().toLowerCase();
  const out = [];
  const add = (label, hint, run) => out.push({ label, hint, run });
  for (const it of state.items) {
    if (it.hidden) continue;
    if (isActive(it)) add(`Stop ${it.alias}`, it.title, () => act(it, 'stop', {}));
    else if (it.kind !== 'link') add(`Run ${it.alias}`, it.title, () => act(it, 'run', { variant: '' }));
    if (it.kind === 'link') add(`Open ${it.alias}`, it.title, () => api('POST', `/api/items/${encodeURIComponent(it.id)}/open`).catch(fail));
    add(`Show ${it.alias}`, 'details, history and output', () => select(it.id));
  }
  add('New item', 'n', () => openEditor(null));
  add('Home', 'pinned items and sections', () => setMode('home'));
  add('All items', 'everything, grouped', () => setMode('all'));
  for (const [id, label] of [['tags', 'Tags'], ['sections', 'Sections'], ['kinds', 'Kinds'], ['ports', 'Ports'], ['import', 'Import…'], ['export', 'Export…'], ['trash', 'Trash']]) {
    add(label, 'manage', () => $(id).click());
  }
  add('Select several items', 'bulk run, stop, tag, delete', () => setSelectMode(true));
  for (const v of state.views) add(`Section: ${v.name}`, v.query, () => applySection(v));
  if (!word) return out.slice(0, 50);
  return out.map((e) => ({ e, s: Math.max(matchScore(e.label, word), matchScore(e.hint, word) / 2) }))
    .filter((x) => x.s > 0).sort((a, b) => b.s - a.s).slice(0, 50).map((x) => x.e);
}

function drawPalette() {
  const list = paletteEntries($('palette-input').value);
  paletteIndex = Math.min(paletteIndex, Math.max(list.length - 1, 0));
  $('palette-list').replaceChildren(...list.map((e, i) => el('button', {
    type: 'button', role: 'option', class: i === paletteIndex ? 'active' : '', 'aria-selected': String(i === paletteIndex),
    onclick: () => { closePalette(); e.run(); },
  }, el('span', {}, e.label), el('span', { class: 'hint' }, e.hint || ''))));
  return list;
}

function openPalette() {
  $('palette').hidden = false;
  $('palette-input').value = '';
  paletteIndex = 0;
  drawPalette();
  $('palette-input').focus();
}

function closePalette() { $('palette').hidden = true; }

$('palette-input').addEventListener('input', () => { paletteIndex = 0; drawPalette(); });
$('palette-input').addEventListener('keydown', (e) => {
  const list = paletteEntries($('palette-input').value);
  if (e.key === 'ArrowDown') { e.preventDefault(); paletteIndex = Math.min(paletteIndex + 1, list.length - 1); drawPalette(); }
  else if (e.key === 'ArrowUp') { e.preventDefault(); paletteIndex = Math.max(paletteIndex - 1, 0); drawPalette(); }
  else if (e.key === 'Enter') { e.preventDefault(); const pick = list[paletteIndex]; closePalette(); if (pick) pick.run(); }
  else if (e.key === 'Escape') { e.preventDefault(); closePalette(); }
});
$('palette').addEventListener('click', (e) => { if (e.target === $('palette')) closePalette(); });

document.addEventListener('keydown', (e) => {
  const typing = ['INPUT', 'TEXTAREA', 'SELECT'].includes(document.activeElement.tagName) || document.querySelector('dialog[open]');
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); if ($('palette').hidden) openPalette(); else closePalette(); }
  else if (!typing && $('palette').hidden && e.key === 'n' && !e.ctrlKey && !e.metaKey && !e.altKey) { e.preventDefault(); openEditor(null); }
});

// ---- search, keys, live updates ----------------------------------------------

for (const b of $('mode').querySelectorAll('button')) b.addEventListener('click', () => setMode(b.dataset.mode));
$('group-by').value = state.groupBy;
$('group-by').addEventListener('change', (e) => {
  state.groupBy = e.target.value;
  prefs.set('groupBy', state.groupBy);
  state.collapsed = {};
  render();
});

$('search').addEventListener('input', (e) => { state.query = e.target.value; render(); renderViews(); });

document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape') {
    if (!$('menu').hidden) hideMenu();
    else if (!$('manage-panel').hidden) $('manage-panel').hidden = true;
    else if (!$('drawer').hidden) closeDrawer();
    else if (state.selectMode) setSelectMode(false);
  } else if (e.key === '/' && !['INPUT', 'TEXTAREA', 'SELECT'].includes(document.activeElement.tagName)) {
    e.preventDefault();
    $('search').focus();
  }
});

function connect() {
  const es = new EventSource(`/api/events?t=${encodeURIComponent(token)}`);
  es.addEventListener('hello', () => { $('conn').className = 'conn ok'; scheduleRefresh(); });
  es.addEventListener('attention', (e) => {
    let d = {};
    try { d = JSON.parse(e.data) || {}; } catch { /* ignore */ }
    if (d.itemId && state.selected !== d.itemId) toast(`${d.alias || 'An item'} needs you: ${d.reason || ''}`, 'warn', () => select(d.itemId));
  });
  for (const type of ['item.changed', 'run.started', 'run.exited', 'attention', 'attention.cleared']) {
    es.addEventListener(type, scheduleRefresh);
  }
  es.onerror = () => { $('conn').className = 'conn lost'; };
}

// #item=<id> opens an item (a notification click lands here)
function openFromHash() {
  const m = /^#item=(.+)$/.exec(location.hash);
  if (!m) return;
  const id = decodeURIComponent(m[1]);
  history.replaceState(null, '', location.pathname + location.search);
  if (itemById(id)) select(id);
}
window.addEventListener('hashchange', openFromHash);

connect();
refresh().then(openFromHash);
