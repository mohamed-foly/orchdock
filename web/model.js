// Pure logic of the management window: search, grouping and ordering.
// No DOM access here, so it can be tested with `node --test web/`.

export const KIND_ORDER = ['service', 'task', 'shell', 'agent', 'link'];

/** Split a search line into words and operators. */
export function parseQuery(text) {
  const q = { words: [], tags: [], kinds: [], running: false, failed: false, idle: false };
  for (const tok of String(text || '').toLowerCase().split(/\s+/).filter(Boolean)) {
    const i = tok.indexOf(':');
    const name = i > 0 ? tok.slice(0, i) : '';
    const val = i > 0 ? tok.slice(i + 1) : '';
    if (name === 'tag' && val) q.tags.push(val);
    else if (name === 'kind' && val) q.kinds.push(val);
    else if ((name === 'project' || name === 'section') && val) q.tags.push(val); // older searches: projects are tags now
    else if (name === 'is' && val === 'running') q.running = true;
    else if (name === 'is' && val === 'failed') q.failed = true;
    else if (name === 'is' && val === 'idle') q.idle = true;
    else q.words.push(tok);
  }
  return q;
}

export function isActive(item) {
  const s = item.status && item.status.state;
  return s === 'running' || s === 'held';
}

export function isFailed(item) {
  const last = (item.status && item.status.last) || '';
  return last.startsWith('failed') || last === 'start-failed';
}

/** How well `word` matches `text`: exact, prefix, word prefix, substring, letters in order, none. */
export function matchScore(text, word) {
  text = String(text || '').toLowerCase();
  if (!text) return 0;
  if (text === word) return 100;
  if (text.startsWith(word)) return 60;
  if (text.split(/[\s\-_/\\.]+/).some((p) => p.startsWith(word))) return 40;
  if (text.includes(word)) return 25;
  let i = 0;
  for (const ch of text) if (i < word.length && word[i] === ch) i++;
  return i === word.length ? 10 : 0;
}

/** Name of the group for items without tags. */
export const UNTAGGED = 'Untagged';
/** Kept for older callers: the "no group" label. */
export const GENERAL = UNTAGGED;

/** 0 when the item does not match the query, otherwise a score (1 without words). */
export function score(item, q) {
  const state = (item.status && item.status.state) || 'idle';
  if (q.running && !isActive(item)) return 0;
  if (q.failed && !isFailed(item)) return 0;
  if (q.idle && state !== 'idle') return 0;
  const kname = String(item.kindName || '').toLowerCase();
  for (const k of q.kinds) if (!String(item.kind).startsWith(k) && !kname.startsWith(k)) return 0;
  const tags = item.tags || [];
  for (const t of q.tags) {
    if (t === 'none' ? tags.length : !tags.some((x) => x.startsWith(t))) return 0;
  }
  if (!q.words.length) return 1;
  const cmd = item.command || {};
  const fields = [[item.alias, 30], [item.title, 20], [tags.join(' '), 20], [cmd.cwd, 10], [item.notes, 5]];
  let total = 0;
  for (const w of q.words) {
    let best = 0;
    for (const [text, weight] of fields) best = Math.max(best, matchScore(text, w) * weight);
    if (!best) return 0;
    total += best;
  }
  return total;
}

function sortList(list, q) {
  return list.sort((a, b) => {
    if (q.words.length && a.s !== b.s) return b.s - a.s;
    if (!!a.item.pinned !== !!b.item.pinned) return a.item.pinned ? -1 : 1;
    if (isActive(a.item) !== isActive(b.item)) return isActive(a.item) ? -1 : 1;
    return a.item.alias < b.item.alias ? -1 : a.item.alias > b.item.alias ? 1 : 0;
  });
}

function header(key, g, list, collapsed) {
  const c = counts(list.map((x) => x.item));
  return {
    type: 'header', group: key, label: g.label, tag: g.tag, viewId: g.viewId, color: g.color, count: list.length,
    running: c.running, failed: c.failed, collapsed: !!collapsed[key], ids: list.map((x) => x.item.id),
  };
}

/**
 * Rows for the full list (All): items that match the query, grouped under
 * headers; collapsed groups show only their header.
 *
 * opts.groupBy is 'tag', 'kind' (default) or 'none'; opts.tags holds the tag
 * definitions (their order comes first, then other tags alphabetically).
 * Grouped by tag, an item appears under each of its tags and items without
 * tags come last.
 *
 * Row: {type:'header', group, label, tag?, color?, count, running, failed, collapsed, ids}
 *    | {type:'item', group, item}
 */
export function buildRows(items, query, collapsed = {}, opts = {}) {
  const q = typeof query === 'string' ? parseQuery(query) : query;
  const groupBy = opts.groupBy === 'project' ? 'tag' : opts.groupBy || 'kind';
  const kinds = opts.kinds || [];
  const defs = opts.tags || [];
  const kdef = new Map(kinds.map((k) => [k.id, k]));
  const tdef = new Map(defs.map((d) => [d.name, d]));
  items = items.map((it) => ({ ...it, kindName: kindLabel(it, kdef) }));
  const groups = new Map();
  const groupsOf = (item) => {
    if (groupBy === 'none') return [{ key: 'all', label: 'All items' }];
    if (groupBy === 'tag') {
      const tags = item.tags || [];
      if (!tags.length) return [{ key: 't:', label: UNTAGGED, tag: '' }];
      // a search by tag shows only the groups of the tags searched for
      const shown = q.tags.length ? tags.filter((t) => q.tags.some((w) => t.startsWith(w))) : tags;
      return shown.map((t) => ({ key: `t:${t}`, label: t, tag: t, color: tdef.get(t) && tdef.get(t).color }));
    }
    if (item.kindId && kdef.has(item.kindId)) return [{ key: `k:${item.kindId}`, label: kdef.get(item.kindId).name, color: kdef.get(item.kindId).color }];
    const own = kdef.get(item.kind);
    const renamed = own && own.name && own.name !== item.kind;
    return [{ key: `${item.kind}s`, label: renamed ? own.name : `${item.kind}s`, color: own && own.color }];
  };
  for (const item of items) {
    if (item.hidden) continue;
    const s = score(item, q);
    if (!s) continue;
    for (const g of groupsOf(item)) {
      if (!groups.has(g.key)) groups.set(g.key, { ...g, list: [] });
      groups.get(g.key).list.push({ item, s });
    }
  }
  const order = (key) => {
    if (groupBy === 'tag') {
      if (key === 't:') return [2, ''];
      const i = defs.findIndex((d) => `t:${d.name}` === key);
      return i >= 0 ? [0, String(i).padStart(6, '0')] : [1, key];
    }
    if (groupBy === 'kind') {
      const i = KIND_ORDER.findIndex((k) => `${k}s` === key);
      if (i >= 0) return [0, String(i).padStart(6, '0')];
      const j = kinds.findIndex((k) => `k:${k.id}` === key);
      return [1, String(j < 0 ? kinds.length : j).padStart(6, '0')];
    }
    return [0, ''];
  };
  const cmp = (a, b) => {
    const [x, y] = [order(a), order(b)];
    return x[0] - y[0] || (x[1] < y[1] ? -1 : x[1] > y[1] ? 1 : 0);
  };
  const rows = [];
  for (const key of [...groups.keys()].sort(cmp)) {
    const g = groups.get(key);
    const list = sortList(g.list, q);
    rows.push(header(key, g, list, collapsed));
    if (collapsed[key]) continue;
    for (const { item } of list) rows.push({ type: 'item', group: key, item });
  }
  return rows;
}

/**
 * Rows for Home: pinned items first, then one group per section (a saved
 * search), in the sections' order. An item can appear in several sections.
 * A search typed on Home narrows every group. Empty sections still show
 * their header, so they can be found and used.
 */
export function buildHome(items, query, collapsed = {}, opts = {}) {
  const q = typeof query === 'string' ? parseQuery(query) : query;
  const kinds = opts.kinds || [];
  const kdef = new Map(kinds.map((k) => [k.id, k]));
  items = items.filter((it) => !it.hidden).map((it) => ({ ...it, kindName: kindLabel(it, kdef) }));
  const rows = [];
  const add = (key, g, list) => {
    sortList(list, q);
    rows.push(header(key, g, list, collapsed));
    if (!collapsed[key]) for (const { item } of list) rows.push({ type: 'item', group: key, item });
  };
  const pinned = [];
  for (const item of items) {
    const s = item.pinned ? score(item, q) : 0;
    if (s) pinned.push({ item, s });
  }
  if (pinned.length) add('pinned', { label: 'Pinned' }, pinned);
  for (const v of opts.views || []) {
    const vq = parseQuery(`${v.query} ${typeof query === 'string' ? query : ''}`);
    const list = [];
    for (const item of items) {
      const s = score(item, vq);
      if (s) list.push({ item, s });
    }
    const tag = (/^\s*tag:(\S+)\s*$/i.exec(v.query) || [])[1];
    add(`v:${v.id}`, { label: v.name, viewId: v.id, tag: tag ? tag.toLowerCase() : undefined }, list);
  }
  return rows;
}

/**
 * The item's main tag, which gives it its default folder and icon: its first
 * tag with a folder, else its first tag with its own icon. A tag with only a
 * colour is never the main tag.
 */
export function mainTag(item, defs) {
  const by = new Map((defs || []).map((d) => [d.name, d]));
  const own = (item.tags || []).map((t) => by.get(t)).filter(Boolean);
  return own.find((d) => d.path) || own.find((d) => d.icon && d.icon.primary) || null;
}

/** Counts for the header line. */
export function counts(items) {
  let total = 0;
  let running = 0;
  let failed = 0;
  for (const it of items) {
    if (it.hidden) continue;
    total++;
    if (isActive(it)) running++;
    if (isFailed(it)) failed++;
  }
  return { total, running, failed };
}

/** Command variants of an item: the implicit default first, then the user's. */
export function variantsOf(item) {
  const own = (item.command && item.command.variants) || [];
  const list = [{ id: 'default', label: 'Default' }];
  for (const v of own) if (v.id !== 'default') list.push(v);
  else if (v.label) list[0] = v;
  return list;
}

/** Parse "KEY=VALUE" lines into an object; returns {env, error}. */
export function parseEnvText(text) {
  const env = {};
  for (const [n, raw] of String(text || '').split(/\r?\n/).entries()) {
    const line = raw.trim();
    if (!line || line.startsWith('#')) continue;
    const i = line.indexOf('=');
    if (i <= 0) return { env: null, error: `line ${n + 1}: expected KEY=VALUE` };
    env[line.slice(0, i).trim()] = line.slice(i + 1);
  }
  return { env, error: '' };
}

export function envToText(env) {
  return Object.entries(env || {}).map(([k, v]) => `${k}=${v}`).join('\n');
}

/** Split a comma or space separated tag list. */
export function parseTags(text) {
  return String(text || '').split(/[\s,]+/).map((t) => t.trim().toLowerCase()).filter(Boolean);
}

const GLYPHS = {
  agent: '✦', favicon: '◌', 'builtin:server': '▤', 'builtin:play': '▶', 'builtin:terminal': '›', 'builtin:link': '↗',
  'builtin:download': '⇣', 'builtin:folder': '▭', 'builtin:kanban': '▥',
};

/** Built-in Lucide icons (web/icons/lucide, see web/icons/NOTICE.md), drawn in the text colour. */
export const BUILTIN_ICONS = ["bell","book-open","bot","box","bug","calendar","clock","cloud","code","database","download","flask-conical","folder","git-branch","globe","hammer","kanban","link","mail","monitor","package","play","rocket","server","shield","terminal","wrench","zap"];

/** Bundled agent logos (see web/icons/NOTICE.md), by built-in icon reference. */
const LOGOS = { 'builtin:claude-code': 'icons/claudecode-color.svg', 'builtin:codex': 'icons/codex-color.svg' };

/** Which bundled logo fits an agent's command ("claude -c" → Claude Code), or ''. */
export function agentLogo(item) {
  const c = item.command || {};
  const first = String((c.argv && c.argv[0]) || c.line || '').trim().split(/\s+/)[0] || '';
  const prog = first.replace(/^.*[\\/]/, '').replace(/\.(exe|cmd|bat|ps1)$/i, '').toLowerCase();
  if (prog === 'claude') return 'builtin:claude-code';
  if (prog === 'codex') return 'builtin:codex';
  return '';
}

/**
 * One icon reference as something to draw:
 * {type:'file', name} | {type:'text', text, color} | {type:'glyph', text} | null.
 */
export function iconPart(ref) {
  if (!ref) return null;
  if (LOGOS[ref]) return { type: 'asset', src: LOGOS[ref] };
  if (ref.startsWith('builtin:') && BUILTIN_ICONS.includes(ref.slice(8))) return { type: 'mask', src: `icons/lucide/${ref.slice(8)}.svg` };
  if (ref.startsWith('file:')) return { type: 'file', name: ref.slice(5) };
  if (ref.startsWith('text:')) {
    const m = /^text:([^:]{1,8}):#?([0-9a-fA-F]{6})$/.exec(ref);
    return m ? { type: 'text', text: m[1], color: `#${m[2]}` } : null;
  }
  if (ref.startsWith('emoji:')) return { type: 'glyph', text: ref.slice(6) };
  return GLYPHS[ref] ? { type: 'glyph', text: GLYPHS[ref] } : null;
}

/** A tag's own icon: its chosen image, the image found in its folder for "auto", or its initials. */
export function tagIcon(def) {
  const ref = def.icon && def.icon.primary;
  const monogram = { type: 'text', text: initials(def.name), color: def.color || '#64748b' };
  if (ref === 'auto') return def.autoIcon ? { type: 'file', name: def.autoIcon } : monogram;
  return iconPart(ref) || monogram;
}
/** Older name of tagIcon. */
export const projectIcon = tagIcon;

/**
 * What to draw for an item: its main icon and at most one badge. An item
 * icon of "project" (stored name, shown as "main tag") uses the icon of the
 * item's main tag (see mainTag), or that tag's initials.
 */
export function itemIcon(item, main) {
  const ic = item.icon || {};
  let primary = null;
  if (ic.primary === 'project') {
    primary = main ? tagIcon(main) : null;
  } else {
    primary = iconPart(ic.primary);
  }
  // the generic "agent" badge becomes the agent's own logo when its command shows which agent it is
  const logo = item.kind === 'agent' ? agentLogo(item) : '';
  // a service's own web icon, once fetched, replaces the generic server badge
  const fav = item.favicon ? `file:${item.favicon}` : '';
  const swap = (b) => {
    if (b === 'agent' && logo) return logo;
    if ((b === 'favicon' || b === 'builtin:server') && fav) return fav;
    return b;
  };
  if (ic.primary === 'favicon' && fav) primary = iconPart(fav);
  const refs = (ic.badges || []).map(swap);
  if (logo && !refs.length) refs.push(logo);
  const badge = refs.map(iconPart).find(Boolean) || null;
  if (!primary && badge) return { primary: badge, badge: null };
  return { primary, badge };
}

export function initials(name) {
  const words = String(name || '').split(/[\s\-_]+/).filter(Boolean);
  if (!words.length) return '?';
  return (words.length > 1 ? words[0][0] + words[1][0] : words[0].slice(0, 2)).toUpperCase();
}

/** The name of an item's kind as the user sees it. */
export function kindLabel(item, kdef) {
  const k = (item.kindId && kdef.get(item.kindId)) || kdef.get(item.kind);
  return (k && k.name) || item.kind;
}

/** "today 18:30", "tomorrow 09:00", "Wed 09:00" (this week) or "12 Mar 09:00". */
export function formatNext(when, now = new Date()) {
  const d = new Date(when);
  if (Number.isNaN(d.getTime()) || d.getFullYear() < 2000) return '';
  const hm = `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
  const day = (x) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const diff = Math.round((day(d) - day(now)) / 86400000);
  if (diff === 0) return `today ${hm}`;
  if (diff === 1) return `tomorrow ${hm}`;
  if (diff > 1 && diff < 7) return `${d.toLocaleDateString('en-GB', { weekday: 'short' })} ${hm}`;
  return `${d.toLocaleDateString('en-GB', { day: 'numeric', month: 'short' })} ${hm}`;
}

/** The text form of a schedule, as typed in the editor ("daily 09:00"). */
export function scheduleSpec(s) {
  if (!s) return '';
  switch (s.kind) {
    case 'interval': return `every ${s.every}`;
    case 'daily': return `daily ${(s.times || []).join(',')}`;
    case 'weekly': return `weekly ${(s.days || []).join(',')} ${s.time}`;
    case 'monthly': return `monthly ${s.day} ${s.time}`;
    case 'cron': return `cron "${s.expr}"`;
    default: return '';
  }
}
