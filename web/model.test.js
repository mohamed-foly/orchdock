import test from 'node:test';
import assert from 'node:assert/strict';
import {
  parseQuery, score, buildRows, buildHome, mainTag, counts, matchScore, variantsOf, parseEnvText, envToText, parseTags,
} from './model.js';

const item = (alias, kind, state = 'idle', last = '', extra = {}) => ({
  id: `i_${alias}`, alias, title: `Title of ${alias}`, kind, tags: [], status: { state, last }, command: {}, ...extra,
});
const names = (rows) => rows.map((r) => (r.type === 'header' ? `[${r.group}]` : r.item.alias));

test('parseQuery splits words and operators', () => {
  assert.deepEqual(parseQuery('  Harbor TAG:Daily kind:svc is:running is:failed is:idle foo:bar '), {
    words: ["harbor", "foo:bar"], tags: ['daily'], kinds: ['svc'], running: true, failed: true, idle: true,
  });
  assert.deepEqual(parseQuery('tag: kind:').words, ['tag:', 'kind:']);
  assert.deepEqual(parseQuery(undefined).words, []);
});

test('matchScore ranks exact > prefix > word prefix > substring > subsequence', () => {
  const s = [
    matchScore('board', 'board'), matchScore('board-main', 'board'), matchScore('my-board', 'board'),
    matchScore('xxboardxx', 'board'), matchScore('b-o-a-r-d', 'board'),
  ];
  for (let i = 1; i < s.length; i++) assert.ok(s[i] > 0 && s[i] < s[i - 1], `${i}: ${s}`);
  assert.equal(matchScore('board', 'zzz'), 0);
  assert.equal(matchScore('', 'a'), 0);
});

test('score applies operators and words', () => {
  const svc = item('web-board', 'service', 'running', '', { tags: ['backlog', 'web'], command: { cwd: 'C:\\work\\shop' } });
  const task = item('pull', 'task', 'idle', 'failed (3)', { tags: ['daily'], notes: 'fetch all' });
  const m = (q, it) => score(it, parseQuery(q)) > 0;
  assert.ok(m('', svc));
  assert.ok(m('web board', svc));
  assert.ok(!m('web nothing', svc));
  assert.ok(m('tag:back', svc) && !m('tag:daily', svc));
  assert.ok(m('kind:serv', svc) && !m('kind:task', svc));
  assert.ok(m('is:running', svc) && !m('is:running', task));
  assert.ok(m('is:failed', task) && !m('is:failed', svc));
  assert.ok(m('is:idle', task) && !m('is:idle', svc));
  assert.ok(m('fetch', task) && m('pll', task) && m("web", svc));
  assert.ok(score(item('deploy', 'task'), parseQuery('deploy')) > score(item('zzz', 'task', 'idle', '', { notes: 'deploy' }), parseQuery('deploy')));
});

test('buildRows groups by kind in a fixed order and sorts inside groups', () => {
  const pinned = item('zz-pinned', 'task', 'idle', '', { pinned: true });
  const rows = buildRows([
    item('zeta', 'shell'), item('alpha', 'service'), item('mid', 'task'), item('beta', 'service', 'running'),
    pinned, item('held', 'task', 'held'),
  ], '');
  assert.deepEqual(names(rows), ['[services]', 'beta', 'alpha', '[tasks]', 'zz-pinned', 'held', 'mid', '[shells]', 'zeta']);
});

test('buildRows filters, ranks by score when searching and hides empty groups', () => {
  const notes = item('aaa', 'task', 'idle', '', { notes: 'deploy' });
  const rows = buildRows([notes, item('deploy', 'task'), item('deploy-all', 'task', 'running'), item('other', 'service')], 'deploy');
  assert.deepEqual(names(rows), ['[tasks]', 'deploy', 'deploy-all', 'aaa']);
  assert.deepEqual(names(buildRows([item('a', 'task'), item('b', 'service', 'running')], 'is:running')), ['[services]', 'b']);
  assert.deepEqual(buildRows([item('a', 'task')], 'nomatch'), []);
});

test('buildRows hides hidden items and collapses groups', () => {
  const rows = buildRows([item('a', 'service'), item('b', 'task'), item('c', 'task'), item('h', 'task', 'idle', '', { hidden: true })], '', { tasks: true });
  assert.deepEqual(names(rows), ['[services]', 'a', '[tasks]']);
  assert.equal(rows[2].collapsed, true);
  assert.equal(rows[2].count, 2);
});

test('counts', () => {
  assert.deepEqual(counts([
    item('a', 'service', 'running'), item('b', 'task', 'held'), item('c', 'task', 'idle', 'failed (2)'),
    item('d', 'task', 'idle', 'start-failed'), item('e', 'task', 'interrupted', 'ok'),
  ]), { total: 5, running: 2, failed: 2 });
});

test('variantsOf puts the default first and keeps the user order', () => {
  assert.deepEqual(variantsOf(item('a', 'task')).map((v) => v.id), ['default']);
  const v = variantsOf(item('a', 'task', 'idle', '', { command: { variants: [{ id: 'remote', label: 'R' }, { id: 'bypass' }] } }));
  assert.deepEqual(v.map((x) => x.id), ['default', 'remote', 'bypass']);
});

test('env text round trip and errors', () => {
  assert.deepEqual(parseEnvText('A=1\n# c\n\nB = x=y \n'), { env: { A: '1', 'B': ' x=y' }, error: '' });
  assert.match(parseEnvText('A=1\nbad').error, /line 2/);
  assert.equal(envToText({ A: '1', B: '2' }), 'A=1\nB=2');
  assert.equal(envToText(undefined), '');
});

test('parseTags', () => {
  assert.deepEqual(parseTags(' Daily, git  web,,'), ['daily', 'git', 'web']);
});

test('buildRows groups by tag: an item under each tag, defined tags first, Untagged last', () => {
  const tags = [{ name: 'temp' }, { name: 'shop', color: '#8b5cf6' }];
  const items = [
    item('loose', 'task'),
    item('srv', 'service', 'running', '', { tags: ['shop', 'web'] }),
    item('claude', 'agent', 'idle', 'failed (1)', { tags: ['shop'] }),
    item('notify', 'service', 'idle', '', { tags: ['temp'] }),
  ];
  const rows = buildRows(items, '', {}, { groupBy: 'tag', tags });
  assert.deepEqual(rows.filter((r) => r.type === 'header').map((r) => `${r.label}:${r.count}`), ['temp:1', 'shop:2', 'web:1', 'Untagged:1']);
  const shop = rows.find((r) => r.label === 'shop');
  assert.equal(shop.running, 1);
  assert.equal(shop.failed, 1);
  assert.equal(shop.tag, 'shop');
  assert.equal(shop.color, '#8b5cf6');
  assert.deepEqual(shop.ids, ['i_srv', 'i_claude']);
  assert.deepEqual(names(buildRows(items, 'tag:sh', {}, { groupBy: 'tag', tags })), ['[t:shop]', 'srv', 'claude']);
  assert.deepEqual(names(buildRows(items, 'tag:none', {}, { groupBy: 'tag', tags })), ['[t:]', 'loose']);
  assert.deepEqual(names(buildRows(items, 'project:temp', {}, { groupBy: 'project', tags })), ['[t:temp]', 'notify']);
  assert.deepEqual(names(buildRows(items, '', { 't:shop': true }, { groupBy: 'tag', tags })).slice(0, 4), ['[t:temp]', 'notify', '[t:shop]', '[t:web]']);
  assert.deepEqual(names(buildRows(items, '', {}, { groupBy: 'none' })), ['[all]', 'srv', 'claude', 'loose', 'notify']);
});

test('buildHome shows pinned items, then each section in order, narrowed by the search', () => {
  const items = [
    item('srv', 'service', 'running', '', { tags: ['shop'], pinned: true }),
    item('claude', 'agent', 'idle', '', { tags: ['shop', 'lab'] }),
    item('notify', 'service', 'idle', '', { tags: ['temp'] }),
    item('gone', 'task', 'idle', '', { tags: ['shop'], hidden: true }),
  ];
  const views = [{ id: 'v_1', name: 'Lab', query: 'tag:lab' }, { id: 'v_2', name: 'Shop', query: 'tag:shop' }, { id: 'v_3', name: 'Empty', query: 'tag:nothing' }];
  const rows = buildHome(items, '', {}, { views });
  assert.deepEqual(names(rows), ['[pinned]', 'srv', '[v:v_1]', 'claude', '[v:v_2]', 'srv', 'claude', '[v:v_3]']);
  assert.equal(rows.find((r) => r.group === 'v:v_2').tag, 'shop');
  assert.equal(rows.find((r) => r.group === 'v:v_2').viewId, 'v_2');
  assert.deepEqual(names(buildHome(items, 'claude', {}, { views })), ['[v:v_1]', 'claude', '[v:v_2]', 'claude', '[v:v_3]']);
  assert.deepEqual(names(buildHome(items, '', { pinned: true }, { views: [] })), ['[pinned]']);
});

test('mainTag: a tag with a folder, else one with its own icon', () => {
  const defs = [{ name: 'web', color: '#f00' }, { name: 'ops', icon: { primary: 'builtin:server' } }, { name: 'shop', path: 'D:/shop' }];
  assert.equal(mainTag({ tags: ['web', 'ops', 'shop'] }, defs).name, 'shop');
  assert.equal(mainTag({ tags: ['web', 'ops'] }, defs).name, 'ops');
  assert.equal(mainTag({ tags: ['x', 'web'] }, defs), null, 'a colour alone does not make a main tag');
});

test('itemIcon resolves main tag icons, monograms and badges', async () => {
  const { itemIcon, iconPart, initials } = await import('./model.js');
  const shop = { name: 'shop', icon: { primary: 'file:shop.png' } };
  const bare = { name: 'medical ocr', color: '#0e7490' };
  assert.deepEqual(iconPart('text:OC:#0e7490'), { type: 'text', text: 'OC', color: '#0e7490' });
  assert.equal(iconPart('text:nope'), null);
  assert.deepEqual(itemIcon({ icon: { primary: 'project', badges: ['file:claude.ico'] } }, shop),
    { primary: { type: 'file', name: 'shop.png' }, badge: { type: 'file', name: 'claude.ico' } });
  assert.deepEqual(itemIcon({ icon: { primary: 'project', badges: ['builtin:server'] } }, bare).primary, { type: 'text', text: 'MO', color: '#0e7490' });
  // no main tag: the badge becomes the main icon
  assert.deepEqual(itemIcon({ icon: { primary: 'project', badges: ['file:claude.ico'] } }, null), { primary: { type: 'file', name: 'claude.ico' }, badge: null });
  assert.equal(initials("build-farm"), "BF");
  assert.equal(initials('temp'), 'TE');
});

test('buildRows groups and searches by the user kinds', () => {
  const kinds = [
    { id: 'agent', name: 'agent', base: 'agent' }, { id: 'service', name: 'service', base: 'service' },
    { id: 'task', name: 'Jobs', base: 'task', color: '#0e7490' }, { id: 'shell', name: 'shell', base: 'shell' },
    { id: 'link', name: 'link', base: 'link' }, { id: 'k_mcp', name: 'MCP server', base: 'service', color: '#db2777' },
  ];
  const items = [item('web', 'service'), item('img', 'service', 'idle', '', { kindId: 'k_mcp' }), item('pull', 'task')];
  const rows = buildRows(items, '', {}, { groupBy: 'kind', kinds });
  assert.deepEqual(rows.filter((r) => r.type === 'header').map((r) => r.label), ['services', 'Jobs', 'MCP server']);
  assert.equal(rows.find((r) => r.label === 'MCP server').color, '#db2777');
  assert.deepEqual(names(buildRows(items, 'kind:mcp', {}, { groupBy: 'kind', kinds })), ['[k:k_mcp]', 'img']);
  assert.deepEqual(names(buildRows(items, 'kind:service', {}, { groupBy: 'kind', kinds })), ['[services]', 'web', '[k:k_mcp]', 'img']);
  assert.deepEqual(names(buildRows(items, 'kind:jobs', {}, { groupBy: 'kind', kinds })), ['[tasks]', 'pull']);
});

test('formatNext and scheduleSpec', async () => {
  const { formatNext, scheduleSpec } = await import('./model.js');
  const now = new Date(2026, 2, 10, 8, 0); // Tue 10 Mar 2026 08:00
  assert.equal(formatNext(new Date(2026, 2, 10, 18, 30), now), 'today 18:30');
  assert.equal(formatNext(new Date(2026, 2, 11, 9, 0), now), 'tomorrow 09:00');
  assert.equal(formatNext(new Date(2026, 2, 13, 9, 0), now), 'Fri 09:00');
  assert.equal(formatNext(new Date(2026, 3, 1, 8, 0), now), '1 Apr 08:00');
  assert.equal(formatNext('0001-01-01T00:00:00Z', now), '');
  assert.equal(scheduleSpec({ kind: 'daily', times: ['09:00', '18:30'] }), 'daily 09:00,18:30');
  assert.equal(scheduleSpec({ kind: 'weekly', days: ['mon', 'thu'], time: '09:00' }), 'weekly mon,thu 09:00');
  assert.equal(scheduleSpec({ kind: 'cron', expr: '*/5 * * * *' }), 'cron "*/5 * * * *"');
  assert.equal(scheduleSpec(null), '');
});

test('agent items get their bundled logo from the command', async () => {
  const { itemIcon, agentLogo } = await import('./model.js');
  const shop = { name: 'shop', color: '#334455' };
  const claude = { kind: 'agent', icon: { primary: 'project', badges: ['agent'] }, command: { line: 'claude --dangerously-skip-permissions -c' } };
  const codex = { kind: 'agent', icon: { primary: 'project', badges: ['agent'] }, command: { shell: 'none', argv: ['C:/tools/codex.exe', 'resume'] } };
  assert.equal(agentLogo(claude), 'builtin:claude-code');
  assert.equal(agentLogo(codex), 'builtin:codex');
  assert.deepEqual(itemIcon(claude, shop).badge, { type: 'asset', src: 'icons/claudecode-color.svg' });
  assert.deepEqual(itemIcon(codex, null), { primary: { type: 'asset', src: 'icons/codex-color.svg' }, badge: null });
  // an explicit badge is kept; other kinds are never guessed
  const own = { kind: 'agent', icon: { primary: 'project', badges: ['file:mine.png'] }, command: { line: 'claude' } };
  assert.deepEqual(itemIcon(own, shop).badge, { type: 'file', name: 'mine.png' });
  const task = { kind: 'task', icon: { primary: 'project', badges: ['builtin:play'] }, command: { line: 'claude -p hi' } };
  assert.equal(itemIcon(task, shop).badge.type, 'mask');
  assert.equal(agentLogo({ command: { line: 'claudette run' } }), '');
});

test('a service shows its own web icon once fetched', async () => {
  const { itemIcon } = await import('./model.js');
  const shop = { name: 'shop', color: '#334455' };
  const svc = { kind: 'service', icon: { primary: 'project', badges: ['builtin:server'] } };
  assert.equal(itemIcon(svc, shop).badge.type, 'mask');
  assert.deepEqual(itemIcon({ ...svc, favicon: 'favicon-i_1.png' }, shop).badge, { type: 'file', name: 'favicon-i_1.png' });
  const own = { kind: 'service', favicon: 'favicon-i_2.ico', icon: { primary: 'favicon', badges: [] } };
  assert.deepEqual(itemIcon(own, shop).primary, { type: 'file', name: 'favicon-i_2.ico' });
  const chosen = { kind: 'service', favicon: 'favicon-i_3.png', icon: { primary: 'project', badges: ['file:mine.png'] } };
  assert.deepEqual(itemIcon(chosen, shop).badge, { type: 'file', name: 'mine.png' });
});

test('built-in icons and automatic tag icons', async () => {
  const { iconPart, tagIcon: projectIcon, BUILTIN_ICONS } = await import('./model.js');
  assert.ok(BUILTIN_ICONS.includes('server') && BUILTIN_ICONS.includes('database'));
  assert.deepEqual(iconPart('builtin:database'), { type: 'mask', src: 'icons/lucide/database.svg' });
  assert.equal(iconPart('builtin:no-such-icon'), null);
  assert.deepEqual(projectIcon({ name: 'shop', icon: { primary: 'auto' }, autoIcon: 'auto-p_1.ico' }), { type: 'file', name: 'auto-p_1.ico' });
  assert.equal(projectIcon({ name: 'shop', icon: { primary: 'auto' } }).type, 'text');
  assert.deepEqual(projectIcon({ name: 'shop', icon: { primary: 'emoji:🛒' } }), { type: 'glyph', text: '🛒' });
});
