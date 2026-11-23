// Drives the management window in a real Chrome through the DevTools protocol:
// loads the page, clicks the controls and checks what happens.
//
//   node tools/uicheck/check.mjs <base-url> <chrome-debug-port> <screenshot-dir>
//
// Chrome must already run with --remote-debugging-port=<port>. The service
// behind <base-url> must hold the sample items the checks expect (see
// tools/uicheck/README.md). Uses only Node's built-in fetch and WebSocket.
import { writeFileSync, mkdirSync } from 'node:fs';
import { join } from 'node:path';

const [base, port, shotDir] = process.argv.slice(2);
if (!base || !port || !shotDir) {
  console.error('usage: node check.mjs <base-url> <chrome-debug-port> <screenshot-dir>');
  process.exit(2);
}
mkdirSync(shotDir, { recursive: true });

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const target = await (await fetch(`http://127.0.0.1:${port}/json/new?about:blank`, { method: 'PUT' })).json();
const ws = new WebSocket(target.webSocketDebuggerUrl);
await new Promise((res, rej) => { ws.onopen = res; ws.onerror = rej; });
let nextId = 0;
const waiting = new Map();
const consoleErrors = [];
ws.onmessage = (m) => {
  const msg = JSON.parse(m.data);
  if (msg.id && waiting.has(msg.id)) { waiting.get(msg.id)(msg); waiting.delete(msg.id); }
  if (msg.method === 'Runtime.exceptionThrown') consoleErrors.push(msg.params.exceptionDetails.text + ' ' + (msg.params.exceptionDetails.exception?.description || ''));
  if (msg.method === 'Runtime.consoleAPICalled' && msg.params.type === 'error') consoleErrors.push(msg.params.args.map((a) => a.value ?? a.description).join(' '));
};
const send = (method, params = {}) => new Promise((resolve, reject) => {
  const id = ++nextId;
  waiting.set(id, (msg) => (msg.error ? reject(new Error(`${method}: ${msg.error.message}`)) : resolve(msg.result)));
  ws.send(JSON.stringify({ id, method, params }));
});
const evaluate = async (expression) => {
  const r = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
  if (r.exceptionDetails) throw new Error(`evaluate failed: ${r.exceptionDetails.exception?.description || r.exceptionDetails.text}`);
  return r.result.value;
};
const waitFor = async (what, expression, ms = 10000) => {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    if (await evaluate(expression).catch(() => false)) return;
    await sleep(100);
  }
  throw new Error(`timed out waiting for ${what}`);
};
const shot = async (name) => {
  const r = await send('Page.captureScreenshot', { format: 'png' });
  writeFileSync(join(shotDir, name), Buffer.from(r.data, 'base64'));
};
const results = [];
const check = (name, ok, detail = '') => {
  results.push({ name, ok });
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? `  (${detail})` : ''}`);
};
const clickLabel = (label) => evaluate(`(() => { const b = [...document.querySelectorAll('button')].find(x => x.getAttribute('aria-label') === ${JSON.stringify(label)} && !x.disabled); if (!b) return false; b.click(); return true; })()`);
const clickIn = (alias, label) => evaluate(`(() => { const r = [...document.querySelectorAll('.row')].find(x => x.querySelector('.alias')?.textContent === ${JSON.stringify(alias)}); const b = r && [...r.querySelectorAll('button')].find(x => x.getAttribute('aria-label') === ${JSON.stringify(label)} && !x.disabled); if (!b) return false; b.click(); return true; })()`);
const rowText = (alias) => `(() => { const r = [...document.querySelectorAll('.row')].find(x => x.querySelector('.alias')?.textContent === ${JSON.stringify(alias)}); return r ? r.innerText : null; })()`;

await send('Runtime.enable');
await send('Page.enable');
await send('Emulation.setDeviceMetricsOverride', { width: 1100, height: 760, deviceScaleFactor: 1, mobile: false });
await send('Page.navigate', { url: base });
await waitFor('the page', `!!document.querySelector('#mode [data-mode=all]')`);
// start from All items whatever an earlier run left in this browser
await evaluate(`document.querySelector('#mode [data-mode=all]').click()`);
await waitFor('the list', `document.querySelectorAll('.row').length > 0`);

// --- the list ---------------------------------------------------------------
const rows = await evaluate(`document.querySelectorAll('.row').length`);
const groups = await evaluate(`[...document.querySelectorAll('.group')].map(g => g.innerText.replace(/\\s+/g, ' ').trim())`);
check('lists every item', rows >= 5, `${rows} rows`);
const setGroup = (v) => evaluate(`(() => { const s = document.getElementById('group-by'); s.value = ${JSON.stringify(v)}; s.dispatchEvent(new Event('change')); })()`);
const groupNames = () => evaluate(`[...document.querySelectorAll('.group')].map(g => g.innerText.replace(/\\s+/g, ' ').trim())`);
check('groups by tag by default, Untagged last', /untagged/i.test(groups[groups.length - 1]), groups.join(' | '));
await setGroup('kind');
const kinds = await groupNames();
check('groups by kind when asked', kinds.length >= 3 && /services/i.test(kinds[0]), kinds.join(' | '));
await setGroup('tag');

// --- search -----------------------------------------------------------------
await evaluate(`(() => { const s = document.getElementById('search'); s.value = 'is:failed'; s.dispatchEvent(new Event('input')); })()`);
const failedRows = await evaluate(`[...document.querySelectorAll('.row .alias')].map(a => a.textContent)`);
check('search filters (is:failed)', failedRows.includes('fail3') && !failedRows.includes('pull-all'), failedRows.join(', '));
await evaluate(`(() => { const s = document.getElementById('search'); s.value = 'tag:git'; s.dispatchEvent(new Event('input')); })()`);
check('search filters (tag:git)', (await evaluate(`document.querySelectorAll('.row').length`)) === 1);
await evaluate(`(() => { const s = document.getElementById('search'); s.value = ''; s.dispatchEvent(new Event('input')); })()`);

// --- run and stop -------------------------------------------------------------
check('clicks Run on an idle shell', await clickIn('scratch', 'Run scratch'));
await waitFor('the shell to be running', `(${rowText('scratch')} || '').includes('running')`);
check('the row updates to running without a reload', true);
await shot('02-running.png');
check('Stop appears for a running item', await clickIn('scratch', 'Stop'));
// "…" (busy) is not idle yet: a shell's stop sequence takes a few seconds.
await waitFor('the shell to be idle again', `/\\bidle\\b/.test(${rowText('scratch')} || '')`, 30000);
check('Stop ends the run', true);

// --- copy ---------------------------------------------------------------------
await evaluate(`Object.defineProperty(navigator, 'clipboard', { value: { writeText: (t) => { window.__copied = t; return Promise.resolve(); } }, configurable: true })`);
check('opens the copy menu', await evaluate(`(() => { const row = [...document.querySelectorAll('.row')].find(x => x.querySelector('.alias')?.textContent === 'pull-all'); const b = row.querySelector('button[aria-label="Copy command"]'); b.click(); return !document.getElementById('menu').hidden; })()`));
await evaluate(`[...document.querySelectorAll('#menu button')].find(b => b.textContent.includes('only')).click()`);
await waitFor('the copied text', `typeof window.__copied === 'string'`);
check('copies the bare command', (await evaluate('window.__copied')) === 'git status', await evaluate('window.__copied'));
await evaluate(`[...document.querySelectorAll('.row')].find(x => x.querySelector('.alias')?.textContent === 'pull-all').querySelector('button[aria-label="Copy command"]').click()`);
await evaluate(`[...document.querySelectorAll('#menu button')][0].click()`);
await waitFor('the full command', `window.__copied.includes('git status') && window.__copied.length > 10`);
check('copies the full command with the folder', /Set-Location|cd /.test(await evaluate('window.__copied')), await evaluate('window.__copied'));

// --- drawer ------------------------------------------------------------------------
await evaluate(`[...document.querySelectorAll('.row')].find(x => x.querySelector('.alias')?.textContent === 'fail3').click()`);
await waitFor('the drawer', `!document.getElementById('drawer').hidden && document.getElementById('drawer-body').innerText.toLowerCase().includes('recent runs')`);
const drawer = await evaluate(`document.getElementById('drawer-body').innerText`);
check('drawer shows history', /failed/.test(drawer) && /Exit/.test(drawer));
check('drawer shows the command', /exit 3/.test(drawer));
await shot('03-drawer.png');
await evaluate(`document.getElementById('drawer-close').click()`);

// --- new / edit / delete --------------------------------------------------------------
await evaluate(`document.getElementById('new').click()`);
await waitFor('the editor', `document.getElementById('editor').open`);
await evaluate(`(() => { const set = (id, v) => { const e = document.getElementById(id); e.value = v; e.dispatchEvent(new Event('input')); e.dispatchEvent(new Event('change')); };
  set('f-kind', 'task'); set('f-title', 'Made in the window'); set('f-alias', 'made-here'); set('f-command', 'echo from the form'); set('f-tags', 'ui, test'); set('f-env', 'A=1\\nB=two'); })()`);
await shot('04-editor.png');
await evaluate(`document.getElementById('editor-save').click()`);
await waitFor('the new item', `(${rowText('made-here')}) !== null`);
check('creates an item from the dialog', true);
// Validation errors from the service are shown, not swallowed.
await evaluate(`document.getElementById('new').click()`);
await waitFor('the editor again', `document.getElementById('editor').open`);
await evaluate(`(() => { const set = (id, v) => { const e = document.getElementById(id); e.value = v; e.dispatchEvent(new Event('input')); }; set('f-title', 'Dup'); set('f-alias', 'made-here'); set('f-command', 'x'); })()`);
await evaluate(`document.getElementById('editor-save').click()`);
await waitFor('the error', `!document.getElementById('editor-error').hidden`);
check('shows the service error for a taken alias', /already used/.test(await evaluate(`document.getElementById('editor-error').textContent`)));
await evaluate(`document.getElementById('editor-cancel').click()`);
// Edit
await evaluate(`[...document.querySelectorAll('button')].find(b => b.getAttribute('aria-label') === 'Edit' && b.closest('.row')?.querySelector('.alias')?.textContent === 'made-here').click()`);
await waitFor('the edit dialog', `document.getElementById('editor').open && document.getElementById('f-title').value === 'Made in the window'`);
await evaluate(`(() => { const e = document.getElementById('f-title'); e.value = 'Renamed in the window'; })()`);
await evaluate(`document.getElementById('editor-save').click()`);
await waitFor('the renamed item', `(${rowText('made-here')} || '').includes('Renamed in the window')`);
check('edits an item', true);
// Delete with confirmation
await evaluate(`[...document.querySelectorAll('button')].find(b => b.getAttribute('aria-label') === 'Delete' && b.closest('.row')?.querySelector('.alias')?.textContent === 'made-here').click()`);
await waitFor('the confirmation', `document.getElementById('confirm').open`);
await evaluate(`document.getElementById('confirm-ok').click()`);
await waitFor('the item to go', `(${rowText('made-here')}) === null`);
check('deletes after confirmation', true);

// --- narrow layout ---------------------------------------------------------------------
await send('Emulation.setDeviceMetricsOverride', { width: 360, height: 740, deviceScaleFactor: 2, mobile: true });
await sleep(400);
const overflow = await evaluate(`document.documentElement.scrollWidth - document.documentElement.clientWidth`);
check('no horizontal scroll at 360 px', overflow <= 1, `overflow ${overflow}px`);
await shot('05-narrow.png');
await send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: 'dark' }] });
await send('Emulation.setDeviceMetricsOverride', { width: 1100, height: 760, deviceScaleFactor: 1, mobile: false });
await sleep(300);
const bg = await evaluate(`getComputedStyle(document.body).backgroundColor`);
check('follows the dark colour scheme', bg !== 'rgb(246, 247, 249)', bg);
await shot('06-dark.png');

// --- tags, sections, Home -------------------------------------------------------
const header = (re) => `[...document.querySelectorAll('.ghead')].find(g => ${re}.test(g.querySelector('.group')?.innerText || ''))`;
await evaluate(`document.getElementById('tags').click()`);
await evaluate(`(() => { document.getElementById('t-name').value = 'uicheck-demo'; document.getElementById('t-path').value = ${JSON.stringify(process.cwd())}; document.getElementById('t-add').click(); })()`);
await waitFor('the new tag in the dialog', `!!document.querySelector('#tags-list input[aria-label="Name of the tag uicheck-demo"]')`);
check('creates a tag with a folder from the Tags dialog', true);
await evaluate(`document.querySelector('#tags-dialog button[value=close]').click()`);
await clickIn('scratch', 'Edit');
await waitFor('the editor', `document.getElementById('editor').open`);
await evaluate(`(() => { const c = document.getElementById('f-cwd'); c.value = ${JSON.stringify(join(process.cwd(), 'tools'))}; c.dispatchEvent(new Event('input')); })()`);
await waitFor('the folder suggestion', `[...document.querySelectorAll('#tag-suggest button')].some(b => /uicheck-demo/.test(b.textContent))`);
check('the editor suggests the tag whose folder holds the working folder', !/uicheck-demo/.test(await evaluate(`document.getElementById('f-tags').value`)));
await evaluate(`[...document.querySelectorAll('#tag-suggest button')].find(b => /uicheck-demo/.test(b.textContent)).click()`);
check('a suggestion is added only when clicked', /uicheck-demo/.test(await evaluate(`document.getElementById('f-tags').value`)));
await evaluate(`(() => { const c = document.getElementById('f-cwd'); c.value = ''; document.getElementById('editor-save').click(); })()`);
await waitFor('scratch under the tag', `(() => { const h = ${header('/uicheck-demo/i')}; const n = h && h.nextElementSibling; return !!n && n.querySelector('.alias')?.textContent === 'scratch'; })()`);
check('an item shows under its tag', true);
await evaluate(`${header('/uicheck-demo/i')}.querySelector('.gact button').click()`);
const started = await waitFor('Start all to start scratch', `/running/.test(${rowText('scratch')} || '')`).then(() => true, () => false);
check('Start all starts the tag items', started, started ? '' : await evaluate(`[...document.querySelectorAll('.toast, .ghead')].map(t => t.innerText.replace(/\s+/g, ' ')).join(' | ')`));
await evaluate(`${header('/uicheck-demo/i')}.querySelectorAll('.gact button')[1].click()`);
await waitFor('Stop all to stop scratch', `/\\bidle\\b/.test(${rowText('scratch')} || '')`, 30000);
check('Stop all stops them', true);
await shot('01b-tags.png');

await evaluate(`${header('/uicheck-demo/i')}.querySelector('.gact .more').click()`);
await evaluate(`[...document.querySelectorAll('#menu button')].find(b => /Show on Home/.test(b.textContent)).click()`);
await waitFor('the section toast', `/Added the section/.test(document.getElementById('toasts').innerText)`);
await clickIn('pull-all', 'Pin pull-all to Home');
await waitFor('the pin', `!!document.querySelector('[aria-label="Unpin pull-all from Home"]')`);
await evaluate(`document.querySelector('#mode [data-mode=home]').click()`);
const home = await groupNames();
check('Home shows pinned items first, then the sections', /pinned/i.test(home[0]) && home.some(g => /uicheck-demo/i.test(g)), home.join(' | '));
check('a section lists its items', await evaluate(`(() => { const h = ${header('/uicheck-demo/i')}; return h?.nextElementSibling?.querySelector('.alias')?.textContent === 'scratch'; })()`));
await shot('01c-home.png');
await evaluate(`(() => { window.prompt = () => 'Demo work'; ${header('/uicheck-demo/i')}.querySelector('.gact .more').click(); })()`);
await evaluate(`[...document.querySelectorAll('#menu button')].find(b => /Rename section/.test(b.textContent)).click()`);
await waitFor('the renamed section', `!!${header('/demo work/i')}`);
check('a section can be renamed from its header', true);
await evaluate(`document.getElementById('sections').click()`);
await waitFor('the sections dialog', `document.getElementById('sections-dialog').open && document.querySelectorAll('#sections-list .srow').length === 1`);
await evaluate(`document.querySelector('#sections-list [aria-label^="Remove the section"]').click()`);
await waitFor('the section to go', `!document.querySelector('#sections-list .srow')`);
check('a section can be removed in the Sections dialog', true);
await evaluate(`document.querySelector('#sections-dialog button[value=close]').click()`);
await clickIn('pull-all', 'Unpin pull-all from Home');
await waitFor('an empty Home', `/Home shows your pinned items/.test(document.getElementById('list').innerText)`);
check('an empty Home explains itself', true);
await evaluate(`[...document.querySelectorAll('#list button')].find(b => /Show all items/.test(b.textContent)).click()`);
await waitFor('All items again', `document.querySelectorAll('.row').length >= 5`);

await evaluate(`document.getElementById('tags').click()`);
await evaluate(`document.querySelector('#tags-list [aria-label="Delete the tag uicheck-demo"]').click()`);
await evaluate(`[...document.querySelectorAll('#menu button')].find(b => /Remove it from/.test(b.textContent)).click()`);
await waitFor('the tag to go', `!${header('/uicheck-demo/i')} && !document.querySelector('#tags-list input[aria-label="Name of the tag uicheck-demo"]')`);
check('deletes a tag and takes it off its items', !/uicheck-demo/.test(await evaluate(rowText('scratch'))));
await evaluate(`document.querySelector('#tags-dialog button[value=close]').click()`);
check('shows the summary', /running/.test(await evaluate(`document.getElementById('summary').textContent`)));
check('shows a running service', /running/.test(await evaluate(rowText('web-board'))));
check('shows a failed result', /failed \(3\)/.test(await evaluate(rowText('fail3'))));
await shot('01-wide.png');

// --- kinds, icons, links ---------------------------------------------------------
check('links have an Open button', await evaluate(`!!document.querySelector('[aria-label^="Open docs"]')`));
await evaluate(`document.getElementById('kinds').click()`);
await evaluate(`(() => { document.getElementById('k-name').value = 'uicheck kind'; document.getElementById('k-base').value = 'service'; document.getElementById('k-add').click(); })()`);
await waitFor('the new kind in the dialog', `[...document.querySelectorAll('#kinds-list .krow input:not([type=color])')].some(i => i.value === 'uicheck kind')`);
check('creates a kind from the Kinds dialog', true);
await evaluate(`document.querySelector('#kinds-dialog button[value=close]').click()`);
await clickIn('web-board', 'Edit');
await waitFor('the edit dialog', `document.getElementById('editor').open`);
const kindChoices = await evaluate(`[...document.getElementById('f-kind').options].map(o => o.textContent)`);
check('the editor offers only kinds with the same behaviour', kindChoices.some(t => /uicheck kind/.test(t)) && !kindChoices.some(t => /^Task/.test(t)), kindChoices.join(' | '));
await evaluate(`(() => { const s = document.getElementById('f-kind'); s.value = [...s.options].find(o => /uicheck kind/.test(o.textContent)).value; document.getElementById('editor-save').click(); })()`);
await waitFor('the kind chip', `/uicheck kind/.test(${rowText('web-board')} || '')`);
check('gives an item a user kind', true);
await setGroup('kind');
check('groups by the user kind', (await groupNames()).some(g => /uicheck kind/i.test(g)), (await groupNames()).join(' | '));
await setGroup('tag');
await evaluate(`document.getElementById('kinds').click()`);
await evaluate(`[...document.querySelectorAll('#kinds-list .krow')].find(r => r.querySelectorAll('input')[1].value === 'uicheck kind').querySelector('[aria-label^="Delete"]').click()`);
const moveChoices = await evaluate(`[...document.querySelectorAll('#menu button')].map(b => b.textContent)`);
check('deleting a kind in use asks where its items go', moveChoices.length >= 1 && /service/i.test(moveChoices[0]), moveChoices.join(' | '));
await evaluate(`document.querySelector('#menu button').click()`);
await waitFor('the kind to go', `!/uicheck kind/.test(${rowText('web-board')} || '')`);
check('deletes a kind; its items keep their behaviour', /service/.test(await evaluate(rowText('web-board'))));
await evaluate(`document.querySelector('#kinds-dialog button[value=close]').click()`);
check('items show an icon', await evaluate(`document.querySelectorAll('.row .ico').length`) >= 1);
await shot('07-kinds.png');

// --- schedules and ports -------------------------------------------------------
await evaluate(`window.confirm = () => false`); // never answer the sign-in question for the user
await clickIn('pull-all', 'Edit');
await waitFor('the edit dialog', `document.getElementById('editor').open && !document.getElementById('l-schedule').hidden`);
await evaluate(`(() => { document.getElementById('f-schedule').value = 'daily 09:00'; document.getElementById('f-missed').value = 'catchup'; document.getElementById('editor-save').click(); })()`);
await waitFor('the next run on the row', `/next: (today|tomorrow) 09:00/.test(${rowText('pull-all')} || '')`);
check('sets a schedule from the editor and shows the next run', true);
check('a catch-up schedule says so on the row', /catch-up/.test(await evaluate(rowText('pull-all'))));
await clickIn('pull-all', 'Edit');
await waitFor('the edit dialog again', `document.getElementById('editor').open`);
check('the editor shows the schedule', (await evaluate(`document.getElementById('f-schedule').value`)) === 'daily 09:00');
await evaluate(`(() => { document.getElementById('f-schedule').value = ''; document.getElementById('editor-save').click(); })()`);
await waitFor('the schedule to go', `!/next:/.test(${rowText('pull-all')} || '')`);
await evaluate(`document.getElementById('ports').click()`);
await waitFor('the ports table', `document.querySelectorAll('#ports-list tbody tr').length > 0`);
check('the Ports dialog lists service ports', /web-board/.test(await evaluate(`document.getElementById('ports-list').innerText`)), await evaluate(`document.getElementById('ports-list').textContent`));
await shot('08-ports.png');
await evaluate(`document.querySelector('#ports-dialog button[value=close]').click()`);

// --- attention ------------------------------------------------------------------
await evaluate(`fetch('/api/attention/scratch', { method: 'POST', headers: { 'X-OrchDock-Token': document.querySelector('meta[name=orchdock-token]').content, 'Content-Type': 'application/json' }, body: JSON.stringify({ reason: 'build finished' }) }).then(r => r.status)`);
await waitFor('the attention mark', `/needs you/.test(${rowText('scratch')} || '')`);
check('attention shows on the row', /build finished/.test(await evaluate(rowText('scratch'))));
check('attention shows a toast', /scratch needs you: build finished/.test(await evaluate(`document.getElementById('toasts').innerText`)));
await evaluate(`[...document.querySelectorAll('.row')].find(x => x.querySelector('.alias')?.textContent === 'scratch').click()`);
await waitFor('attention to clear on opening the item', `!/needs you/.test(${rowText('scratch')} || '')`);
check('opening the item clears its attention', true);
await evaluate(`document.getElementById('drawer-close').click()`);

// --- icons, tags, views, bulk, trash, export, palette -------------------------------
await clickIn('web-board', 'Edit');
await waitFor('the editor', `document.getElementById('editor').open`);
await evaluate(`document.getElementById('f-icon').click()`);
await waitFor('the icon picker', `document.getElementById('icon-dialog').open`);
await evaluate(`[...document.querySelectorAll('#ip-panel button')].find(b => b.getAttribute('aria-label') === 'database').click()`);
check('the picker previews the chosen icon', await evaluate(`!!document.querySelector('#ip-preview .lm')`));
await evaluate(`document.getElementById('ip-ok').click()`);
await waitFor('the editor to show the chosen icon', `!document.getElementById('icon-dialog').open && !!document.querySelector('#f-icon .lm')`);
await evaluate(`document.getElementById('editor-save').click()`);
await waitFor('the new icon on the row', `(() => { const r = [...document.querySelectorAll('.row')].find(x => x.querySelector('.alias')?.textContent === 'web-board'); return !!r && /database/.test(r.querySelector('.ico .lm')?.getAttribute('style') || ''); })()`);
check('an item can get a built-in icon from the picker', true);

check('the Manage menu starts closed', await evaluate(`document.getElementById('manage-panel').hidden`));
await evaluate(`document.getElementById('manage').click()`);
check('Manage opens its menu', !(await evaluate(`document.getElementById('manage-panel').hidden`)));
await evaluate(`document.getElementById('tags').click()`);
await waitFor('the tags dialog', `document.getElementById('tags-dialog').open`);
await evaluate(`(() => { const row = document.querySelector('#tags-list input[aria-label="Name of the tag daily"]').closest('.prow'); const c = row.querySelector('input[type=color]'); c.value = '#aa3366'; c.dispatchEvent(new Event('change')); })()`);
await waitFor('the tag colour on the chips', `[...document.querySelectorAll('.row .chip.tagged')].some(c => /#daily/.test(c.textContent))`);
check('a tag colour shows on its chips', true);
await evaluate(`document.querySelector('#tags-dialog button[value=close]').click()`);

await evaluate(`(() => { window.prompt = () => 'Git things'; const s = document.getElementById('search'); s.value = 'tag:git'; s.dispatchEvent(new Event('input')); })()`);
await evaluate(`document.querySelector('#views .save').click()`);
await waitFor('the saved section', `[...document.querySelectorAll('#views .vchip')].some(v => /Git things/.test(v.textContent))`);
await evaluate(`(() => { const s = document.getElementById('search'); s.value = ''; s.dispatchEvent(new Event('input')); })()`);
await evaluate(`[...document.querySelectorAll('#views .vchip')].find(b => b.textContent === 'Git things').click()`);
check('a section chip applies its search', (await evaluate(`document.getElementById('search').value`)) === 'tag:git' && (await evaluate(`document.querySelectorAll('.row').length`)) === 1);
await evaluate(`(() => { window.prompt = () => 'Git things'; document.getElementById('sections').click(); document.querySelector('#sections-list [aria-label="Remove the section Git things"]').click(); })()`);
await waitFor('the section to go', `![...document.querySelectorAll('#views .vchip')].some(v => /Git things/.test(v.textContent))`);
await evaluate(`document.querySelector('#sections-dialog button[value=close]').click()`);
await evaluate(`(() => { const s = document.getElementById('search'); s.value = ''; s.dispatchEvent(new Event('input')); })()`);

await evaluate(`document.getElementById('select-mode').click()`);
check('select mode shows checkboxes and the bulk bar', (await evaluate(`document.querySelectorAll('.row .pick').length`)) >= 4 && !(await evaluate(`document.getElementById('bulk').hidden`)));
await evaluate(`(() => { for (const a of ['scratch', 'pull-all']) [...document.querySelectorAll('.row')].find(x => x.querySelector('.alias')?.textContent === a).querySelector('.pick').click(); })()`);
await evaluate(`(() => { window.prompt = () => 'bulked'; document.querySelector('#bulk [data-bulk=tag]').click(); })()`);
await waitFor('the bulk tag', `[...document.querySelectorAll('.row')].filter(r => /#bulked/.test(r.innerText)).length >= 2`).catch(() => {});
const bulked = await evaluate(`[...document.querySelectorAll('.row')].filter(r => /#bulked/.test(r.innerText)).map(r => r.querySelector('.alias').textContent).filter((a, i, all) => all.indexOf(a) === i).sort().join(',')`);
check('bulk tagging tags the selected items', bulked === 'pull-all,scratch', bulked);
await evaluate(`(() => { window.prompt = () => 'bulked'; document.querySelector('#bulk [data-bulk=untag]').click(); })()`);
await waitFor('the bulk untag', `![...document.querySelectorAll('.row')].some(r => /#bulked/.test(r.innerText))`);
await evaluate(`document.getElementById('select-mode').click()`);

await evaluate(`document.getElementById('trash').click()`);
await waitFor('the trash dialog', `document.getElementById('trash-dialog').open && /made-here/.test(document.getElementById('trash-list').innerText)`);
check('deleted items are in the trash', true);
await evaluate(`[...document.querySelectorAll('#trash-list .trow')].find(r => /made-here/.test(r.innerText)).querySelector('button').click()`);
await waitFor('the restored item', `(${rowText('made-here')}) !== null`);
check('an item can be restored from the trash', true);
await evaluate(`document.querySelector('#trash-dialog button[value=close]').click()`);
await evaluate(`(() => { window.confirm = () => true; [...document.querySelectorAll('button')].find(b => b.getAttribute('aria-label') === 'Delete' && b.closest('.row')?.querySelector('.alias')?.textContent === 'made-here').click(); })()`);
await waitFor('the confirmation', `document.getElementById('confirm').open`);
await evaluate(`document.getElementById('confirm-ok').click()`);
await waitFor('the item to go again', `(${rowText('made-here')}) === null`);
await evaluate(`document.getElementById('trash').click()`);
await waitFor('the trash again', `document.getElementById('trash-dialog').open`);
await evaluate(`document.getElementById('trash-empty').click()`);
await waitFor('an empty trash', `/empty/.test(document.getElementById('trash-list').innerText)`);
check('the trash can be emptied', true);
await evaluate(`document.querySelector('#trash-dialog button[value=close]').click()`);

await evaluate(`(() => { window.confirm = () => false; document.getElementById('export').click(); })()`);
await waitFor('the export toast', `/Exported, without environment values/.test(document.getElementById('toasts').innerText)`);
check('export downloads without environment values by default', true);

await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'k', code: 'KeyK', modifiers: 2, windowsVirtualKeyCode: 75 });
await waitFor('the command palette', `!document.getElementById('palette').hidden`);
await evaluate(`(() => { const i = document.getElementById('palette-input'); i.value = 'scratch'; i.dispatchEvent(new Event('input')); })()`);
check('the palette finds items and actions', /scratch/.test(await evaluate(`document.getElementById('palette-list').innerText`)));
await evaluate(`document.getElementById('palette-input').dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))`);
check('Escape closes the palette', await evaluate(`document.getElementById('palette').hidden`));
await shot('09-organise.png');

check('no script errors', consoleErrors.length === 0, consoleErrors.join(' | '));
ws.close();
const failed = results.filter((r) => !r.ok);
console.log(`\n${results.length - failed.length}/${results.length} checks passed`);
process.exit(failed.length ? 1 : 0);
