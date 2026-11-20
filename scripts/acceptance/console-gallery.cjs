// Reproducible README screenshots from synthetic responses and the real bundle.
// No console, harness, saved project, model API or credential is accessed.
const fs = require('node:fs');
const path = require('node:path');
const { spawn } = require('node:child_process');
const assert = require('node:assert/strict');
const root = path.resolve(__dirname, '../..');
const web = process.argv.includes('--web')
  ? path.resolve(root, process.argv[process.argv.indexOf('--web') + 1])
  : path.join(root, 'internal/web/static');
const defaultOut = path.join(root, '.build/ui-qa');
fs.mkdirSync(defaultOut, { recursive: true });

// Optional capture parameters (defaults keep the original gallery run intact):
//   --pages overview,state,runs   which pages to shoot
//   --viewport 1440x900|390x844   one viewport for every selected page
//   --theme dark|light            class-based theme (.dark) after render
//   --out .build/ui-qa            output directory
const argv = process.argv.slice(2);
const argValue = (name) => {
  const i = argv.indexOf('--' + name);
  return i >= 0 ? argv[i + 1] : undefined;
};
const out = argValue('out') ? path.resolve(root, argValue('out')) : defaultOut;
fs.mkdirSync(out, { recursive: true });
const viewportArg = argValue('viewport');
const themeArg = argValue('theme');
const pagesArg = argValue('pages');
// --search types into the graph filter box before the shot, so a capture can
// show how search matches and the selected focus coexist.
const searchArg = argValue('search');
// --chain path clicks 只看主路径 before the shot (the single-chain view); the
// button lives in the strip on a wide map and in the node panel on a narrow one.
const chainArg = argValue('chain');
// --timeline 40 scrubs the time axis to 40% before the shot, so a still capture
// can show what "not revealed yet" looks like.
const timelineArg = argValue('timeline');
// --allow-overflow keeps the capture going when a page is expected to overflow
// (it is how a before/after pair is taken of the broken vs fixed layout).
const allowOverflow = argv.includes('--allow-overflow');
const [viewportWidth, viewportHeight] = (viewportArg || '1440x1000').split('x').map(Number);
if (!Number.isFinite(viewportWidth) || !Number.isFinite(viewportHeight)) throw Error('bad --viewport: ' + viewportArg);
if (themeArg && themeArg !== 'dark' && themeArg !== 'light') throw Error('bad --theme: ' + themeArg);

// Page table used by both the original gallery and the parameterized runs.
const PAGES = {
  settings: { route: '/g/?view=settings', expected: '统一模型连接', name: 'models.png', height: 1000 },
  workers: { route: '/p/fixture/?view=runs&tab=workers', expected: '核对账户访问边界', name: 'workers.png', height: 1000 },
  projects: { route: '/g/?view=projects', expected: '应用安全评估示例', name: 'projects.png', height: 640 },
  overview: { route: '/p/fixture/?view=overview', expected: '失败运行', name: 'overview.png', height: 900 },
  state: { route: '/p/fixture/?view=state', expected: '执行者 · 意图', name: 'state.png', height: 900 },
  runs: { route: '/p/fixture/?view=runs', expected: '事件流', name: 'runs.png', height: 900 },
  messages: { route: '/p/fixture/?view=messages', expected: '事实质量修正已完成', name: 'messages.png', height: 900 },
  graph: { route: '/p/fixture/?view=graph#f:f_a', expected: '事实', name: 'graph-node.png', height: 900 },
};
const selectedPages = pagesArg ? pagesArg.split(',').map((s) => s.trim()).filter(Boolean) : ['settings', 'workers', 'projects'];
for (const id of selectedPages) if (!PAGES[id]) throw Error('unknown --pages entry: ' + id);
const profileDir = fs.mkdtempSync(path.join(defaultOut, 'gallery-chrome-'));
const chrome = spawn(process.env.TW_CHROME || 'C:/Program Files/Google/Chrome/Application/chrome.exe', [
  '--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
  '--remote-debugging-port=0', '--remote-debugging-address=127.0.0.1',
  '--user-data-dir=' + profileDir, 'about:blank',
], { windowsHide: true, stdio: 'ignore' });
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
const at = '2026-11-16T09:30:00+08:00';
const project = { id: 'fixture', name: '应用安全评估示例', created: at, paused: true,
  scope: 'portal.example.test（离线示例）',
  description: '在隔离示例环境中核对访问控制与证据完整性。',
  intents: 3, facts: 6, evidence: 4, running: 1, max_runs_effective: 3 };
// Historical per-run model metadata (profile/resolved_profile) is not part of
// current runs; synthetic rows carry only what the console still shows today.
const workers = [
  { intent_id: 'in_access', participant_id: 'pt_in_access', title: '核对账户访问边界',
    status: 'running', running: true, can_kill: true, run_id: 'r_access',
    model: 'example-model', node: 'local', created_at: at,
    last_event_kind: 'tool_result', last_event_text: '已记录响应样本，正在核对授权范围与验收条件。', last_event_at: at },
  { intent_id: 'in_evidence', participant_id: 'pt_in_evidence', title: '整理本轮原始证据',
    status: 'waiting', running: false, can_kill: true, run_id: 'r_evidence',
    parent_intent_id: 'in_access', model: 'example-model', node: 'local', created_at: at,
    last_event_kind: 'handoff', last_event_text: '等待关联方向交付原始材料。', last_event_at: at },
  { intent_id: 'in_entry', participant_id: 'pt_in_entry', title: '确认示例环境入口',
    status: 'done', running: false, can_kill: false, run_id: 'r_entry',
    model: 'example-model', node: 'local', created_at: at,
    last_event_kind: 'turn_ended', last_event_text: '入口状态和来源材料已登记。', last_event_at: at },
];
// Read-only fixtures for the three project pages added to the gallery. They are
// only requested by ?view=overview|state|runs and never by the original three
// screenshots, so the existing assertions keep the same inputs.
const fixtureIntents = [
  { id: 'in_access', title: '核对账户访问边界', status: 'running', attempts: 1, created_at: at, updated_at: at,
    description: '在隔离示例环境中核对账户权限边界。', node: 'local', doc: '示例任务书', notes: [] },
  { id: 'in_entry', title: '确认示例环境入口', status: 'done', attempts: 1, created_at: at, updated_at: at,
    description: '登记入口与来源材料。', node: 'local', notes: [] },
  { id: 'in_evidence', title: '整理本轮原始证据', status: 'waiting', attempts: 1, created_at: at, updated_at: at,
    description: '等待关联方向交付原始材料。', node: 'local', notes: [{ kind: 'retry', text: '补齐证据目录', at }] },
  { id: 'in_review', title: '复核访问控制结论', status: 'pending', attempts: 0, created_at: at, updated_at: at,
    description: '复核结论是否成立。', node: 'local', notes: [] },
];
// The running example started 12 minutes before capture, so "quiet for 12m"
// is visible instead of a three-day-old fixture timestamp.
const recent = new Date(Date.now() - 12 * 60 * 1000).toISOString();
const fixtureRuns = [
  { id: 'r_access', intent_id: 'in_access', node: 'local', status: 'running', events: 12, declared_facts: 2,
    tokens: 1840, started_at: recent, session_id: 'session_access', model: 'example-model', provider: 'openai',
    purpose: '', warnings: [] },
  { id: 'r_entry', intent_id: 'in_entry', node: 'local', status: 'done', events: 9, declared_facts: 1,
    tokens: 920, started_at: at, ended_at: at, session_id: 'session_entry', model: 'example-model', purpose: '' },
  { id: 'r_evidence', intent_id: 'in_evidence', node: 'local', status: 'canceled', events: 4, declared_facts: 0,
    tokens: 310, started_at: at, ended_at: at, session_id: 'session_evidence', purpose: '',
    cancel_cause: 'operator' },
  { id: 'r_verify', intent_id: 'in_review', node: 'local', status: 'done', events: 3, declared_facts: 0,
    tokens: 260, started_at: at, ended_at: at, session_id: 'session_verify', purpose: 'verifier',
    warnings: ['示例告警：交接缺少验收条目'] },
];

// A participant whose title is a whole direction name is what breaks the meta
// row: the sender label must truncate instead of pushing the delivery badge off
// the row.
const longTitle = '113.219.171.189:9012 定案补账：admin/123456 管理员会话与未授权海康/GIS/pprof（只读）';
const fixtureMessages = [{
  id: 'm_long_1', seq: 41, project_id: 'fixture',
  from_participant_id: 'pt_long', to_participant_id: 'commander',
  kind: 'say',
  text: '[in_pxy4visls3 收口] 事实质量修正已完成：f_fqqsetvcog 已对齐到 pprof 事实。',
  status: 'accepted', delivery_path: 'prompt', created_at: at,
}];

const fixtureFactOf = (id, title, severity, createdAt = at) => ({
  id, intent_id: '', kind: 'vuln', title,
  summary: '示例：离线合成数据，不含真实项目。',
  confidence: 'observed', status: 'verified', severity,
  proof: 'dynamic', claim_version: 1, created_at: createdAt, evidence_ids: [], refs: [], tags: [],
});
const fixtureFact = fixtureFactOf('f_a', '示例：网关 OPTIONS 绕过鉴权', 'medium');
// The graph page carries a time axis: synthetic rows are spread over one working
// hour so the scrubber has somewhere to travel and the reveal is visible in a
// still capture. Every row is offline sample data.
const timedFact = (id, title, severity, minute) =>
  fixtureFactOf(id, title, severity, `2026-11-16T${String(9 + Math.floor(minute / 60)).padStart(2, '0')}:${String(minute % 60).padStart(2, '0')}:00+08:00`);
const fixtureFacts = [
  timedFact('f_a', '示例：网关 OPTIONS 绕过鉴权', 'medium', 2),
  timedFact('f_b', '示例：网关白名单直通下游 actuator', 'high', 9),
  timedFact('f_c', '示例：登录验证码可绕过', 'low', 16),
  timedFact('f_d', '示例：管理端导出接口缺少角色校验', 'high', 24),
  timedFact('f_e', '示例：对象存储列举权限外露', 'medium', 31),
  timedFact('f_f', '示例：任务调度接口未做会话绑定', 'medium', 38),
  timedFact('f_g', '示例：预发环境调试端口可从办公网访问', 'low', 46),
  timedFact('f_h', '示例：审计日志仅保留七天', 'low', 53),
];
const fixtureGraph = {
  version: 1, generated_at: at,
  snapshot_version: { epoch: 'e_fixture', seq: 1 }, total: 14, has_more: false, next_cursor: '',
  facts: fixtureFacts, goals: [], intents: [
    { id: 'in_gateway', title: '核对网关路由与鉴权', status: 'done', attempts: 1,
      created_at: '2026-11-16T09:05:00+08:00', updated_at: '2026-11-16T09:20:00+08:00', node: 'local', notes: [] },
    { id: 'in_export', title: '验证管理端导出权限', status: 'done', attempts: 1,
      created_at: '2026-11-16T09:22:00+08:00', updated_at: '2026-11-16T09:40:00+08:00', node: 'local', notes: [] },
  ],
  edges: [
    { id: 'e_b_a', source: 'f:f_b', target: 'f:f_a', kind: 'reference' },
    { id: 'e_p1', source: 'f:f_a', target: 's:in_gateway', kind: 'premise' },
    { id: 'e_p2', source: 'f:f_b', target: 's:in_gateway', kind: 'premise' },
    { id: 'e_i1', source: 's:in_gateway', target: 'f:f_c', kind: 'intent' },
    { id: 'e_i2', source: 's:in_gateway', target: 'f:f_d', kind: 'intent' },
    { id: 'e_p3', source: 'f:f_d', target: 's:in_export', kind: 'premise' },
    { id: 'e_i3', source: 's:in_export', target: 'f:f_e', kind: 'intent' },
    { id: 'e_i4', source: 's:in_export', target: 'f:f_f', kind: 'intent' },
  ],
  boundary: [],
  fact_meta: fixtureFacts.map(f => ({ id: f.id, needs_review: false, reviewed_claim_version: 1, evidence_count: 0, ref_count: 0 })),
  facts_total: fixtureFacts.length, intents_total: 2, goals_total: 0, edges_total: 8, boundary_total: 0,
};

function api(url) {
  const route = url.pathname.split('/api/')[1];
  if (route && /^runs\/[^/]+\/events$/.test(route)) {
    return { events: [
      { at: recent, kind: 'tool_result', text: '读取示例账户的授权响应，记录边界字段。' },
      { at: recent, kind: 'tool_call', text: '调用只读接口核对权限。' },
      { at: recent, kind: 'turn_ended', text: '本轮结束。', stop_reason: 'end_turn' },
      { at: recent, kind: 'tool_result', text: '证据样本已登记到示例目录。' },
      { at: recent, kind: 'error', text: '一次只读请求超时，已重试。' },
      { at: recent, kind: 'tool_call', text: '继续核对下一个边界。' },
    ] };
  }
  switch (route) {
    case 'intents': return { intents: fixtureIntents, total: fixtureIntents.length, has_more: false };
    case 'runs': return { runs: fixtureRuns };
    case 'goals': return { goals: [
      { id: 'g_access', statement: '核对边界外主机的访问是否受限', met: false, created_at: at },
      { id: 'g_evidence', statement: '每一条结论都有可复核证据', met: true, created_at: at },
    ] };
    case 'usage/breakdown': return {
      commander: { total: 3200, runs: 4 }, executor: { total: 5100, runs: 9 },
      executor_total: { total: 5100 }, judge: { jev: { total: 400 }, llm: { total: 120 } },
      combined: { total: 8300 }, all: { total: 8820 },
    };
    case 'operator-inbox': return { total: 0 };
    case 'projects': return { projects: [project, { ...project, id: 'fixture-archive', name: '交接归档示例',
      running: 0, intents: 8, facts: 16, evidence: 24 }], max_runs: 3, round_timeout_seconds: 900 };
    case 'settings': return { configured: true, model: 'example-model', api_key_set: true,
      provider:'openai', base_url:'https://gateway.example.test/v1', context_window:128000, path: '~/.cyber/cyber.yaml' };
    case 'workers': return { workers, count: workers.length };
    case 'peers': return { mode: 'open', peer_policy: { mode: 'open', allow: [] } };
    case 'participants': return { participants: [
      ...workers.map(w => ({ id: w.participant_id, title: w.title, intent_id: w.intent_id })),
      { id: 'pt_long', title: longTitle, intent_id: 'in_access' },
    ] };
    case 'messages': return { messages: fixtureMessages };
    case 'graph': return fixtureGraph;
    case 'changes': return { changes: [], has_more: false, next_cursor: '', high_watermark: { epoch: 'e_fixture', seq: 0 }, pending: 0 };
    case 'facts/f_a': return { fact: fixtureFact, evidence: [] };
    case 'instructions': return { instructions: [], has_more: false, next_cursor: '' };
    case 'ioa/events': return { records: [], sessions: {} };
    case 'nodes': return { nodes: [], default: 'local' };
    case 'runtime-settings': return { auxiliary_concurrency: 1 };
    case 'board': return { generated_at: at, case: 'fixture', project_paused: true,
      intents: { total: 4, running: 1, waiting: 1, done: 1, pending: 1, held: 0, failed: 0, parked: 0 },
      facts: { total: 6, verified: 4, by_kind: {}, by_origin: {}, with_evidence: 5, reviewable_unverified: 1, notes: 1 }, evidence: 4,
      runs: { total: 4, running: 1, done: 3, failed: 0, canceled: 1 }, goals: { total: 2, met: 1, open: [] },
      decider: { enabled: true, paused: true, paused_reason: '操作员暂停', rounds: 3, tokens_total: 3200,
        model: 'example-model', context_percent: 42, handoff_missing: 1 },
      playbooks: { confirmed: 2, candidates: 1, retired: 0, archived: 0 }, redispatch: { total: 1 },
      open_intents: [], running_intents: [],
      waiting_intents: [{ id: 'in_evidence', title: '整理本轮原始证据', status: 'waiting' }],
      recent_facts: [
        { id: 'f_1', title: '示例入口可访问', kind: 'observation', status: 'verified', evidence: 2, origin: 'executor' },
        { id: 'f_2', title: '示例账户缺少边界校验', kind: 'observation', status: 'unverified', evidence: 1, origin: 'executor' },
        { id: 'f_3', title: '示例响应带审计头', kind: 'observation', status: 'unverified', evidence: 0, origin: 'executor' },
      ],
      frontier: [
        { id: 's_1', title: '复核边界外主机访问', ready: true, last_declared: 1 },
        { id: 's_2', title: '补齐证据目录', ready: false, blocked_on: ['f_2'], last_declared: 0 },
      ],
      handoff_warnings: [] };
    default: return { runs: [], nodes: [], projects: [], configured: true, model_configured: true };
  }
}

(async () => {
  let ws;
  const errors = [];
  try {
    const portFile = path.join(profileDir, 'DevToolsActivePort');
    for (let i = 0; i < 100 && !fs.existsSync(portFile); i++) await pause(100);
    const port = Number(fs.readFileSync(portFile, 'utf8').split('\n')[0]);
    const pages = await (await fetch('http://127.0.0.1:' + port + '/json/list')).json();
    ws = new WebSocket(pages.find(p => p.type === 'page').webSocketDebuggerUrl);
    await new Promise((resolve, reject) => { ws.addEventListener('open', resolve, { once: true }); ws.addEventListener('error', reject, { once: true }); });
    let seq = 0;
    const pending = new Map();
    const call = (method, params = {}) => new Promise((resolve, reject) => {
      const id = ++seq;
      const timer = setTimeout(() => { pending.delete(id); reject(Error('CDP timeout: ' + method)); }, 10000);
      pending.set(id, { resolve(value) { clearTimeout(timer); resolve(value); }, reject(error) { clearTimeout(timer); reject(error); } });
      ws.send(JSON.stringify({ id, method, params }));
    });
    ws.addEventListener('message', async event => {
      const msg = JSON.parse(event.data);
      if (msg.id) { const p = pending.get(msg.id); if (p) { pending.delete(msg.id); msg.error ? p.reject(Error(JSON.stringify(msg.error))) : p.resolve(msg.result); } return; }
      if (msg.method === 'Runtime.exceptionThrown') errors.push(msg.params.exceptionDetails.exception?.description || msg.params.exceptionDetails.text);
      if (msg.method !== 'Fetch.requestPaused') return;
      const { requestId, request } = msg.params;
      const url = new URL(request.url);
      try {
        let body, type = 'application/json';
        if (url.pathname.startsWith('/static/')) {
          const file = path.resolve(web, url.pathname.slice('/static/'.length));
          assert.ok(file.startsWith(web + path.sep));
          body = fs.readFileSync(file);
          type = file.endsWith('.js') ? 'text/javascript' : file.endsWith('.css') ? 'text/css' : 'application/octet-stream';
        } else if (url.pathname.includes('/api/')) {
          assert.equal(request.method, 'GET', 'gallery may only read fixture APIs');
          body = Buffer.from(JSON.stringify(api(url)));
        } else { body = fs.readFileSync(path.join(web, 'index.html')); type = 'text/html'; }
        await call('Fetch.fulfillRequest', { requestId, responseCode: 200,
          responseHeaders: [{ name: 'Content-Type', value: type }], body: body.toString('base64') });
      } catch (error) {
        errors.push(error.message);
        await call('Fetch.failRequest', { requestId, errorReason: 'Failed' });
      }
    });
    await call('Page.enable'); await call('Runtime.enable');
    await call('Fetch.enable', { patterns: [{ urlPattern: '*' }] });
    await call('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1000, deviceScaleFactor: 1, mobile: false });
    const evaluate = async expression => {
      const r = await call('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
      if (r.exceptionDetails) throw Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text);
      return r.result.value;
    };
    const captured = [];
    for (const id of selectedPages) {
      const page = PAGES[id];
      const width = viewportArg ? viewportWidth : 1440;
      const height = viewportArg ? viewportHeight : page.height;
      await call('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile: false });
      await call('Page.navigate', { url: 'http://tianwu.test' + page.route });
      let ready = false;
      for (let i = 0; i < 100 && !ready; i++) {
        ready = await evaluate('document.body.innerText.includes(' + JSON.stringify(page.expected) + ')');
        if (!ready) await pause(75);
      }
      assert.ok(ready, page.route + ' did not render');
      await pause(250);
      if (searchArg && id === 'graph') {
        // React controls the input: set through the native setter, then dispatch
        // the same input event a keystroke would, or the state never updates.
        const typed = await evaluate(`(() => {
          const el = document.querySelector('input[aria-label="搜索节点"]');
          if (!el) return false;
          const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set;
          set.call(el, ${JSON.stringify(searchArg)});
          el.dispatchEvent(new Event('input', { bubbles: true }));
          return true;
        })()`);
        assert.ok(typed, 'graph search box not found');
        await pause(600);
      }
      if (chainArg === 'path' && id === 'graph') {
        // The single-chain view: the node is already selected by the route hash,
        // so the button exists in the strip (wide) or the node panel (narrow).
        const clicked = await evaluate(`(() => {
          const btn = [...document.querySelectorAll('button')].find((b) => (b.textContent || '').includes('只看主路径'));
          if (!btn) return false;
          btn.click();
          return true;
        })()`);
        assert.ok(clicked, 'chain path button not found');
        await pause(700);
      }
      if (timelineArg && id === 'graph') {
        // Scrub the axis: native setter + input event, the same route a drag takes.
        const scrubbed = await evaluate(`(() => {
          const el = document.querySelector('input[aria-label="按入账顺序揭示节点"]');
          if (!el) return false;
          const last = Number(el.max);
          const next = Math.round((last * ${Number(timelineArg)}) / 100);
          const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set;
          set.call(el, String(next));
          el.dispatchEvent(new Event('input', { bubbles: true }));
          return true;
        })()`);
        assert.ok(scrubbed, 'timeline slider not found');
        await pause(500);
      }
      // The console themes by class (.dark), not by media query: apply it to the
      // document element after the page renders.
      if (themeArg) {
        await evaluate(`document.documentElement.classList.${themeArg === 'dark' ? 'add' : 'remove'}('dark')`);
        await pause(200);
      }
      if (id === 'workers') assert.ok(await evaluate('!document.body.innerText.includes("造人") && !document.body.innerText.includes("造一个人")'));
      const overflow = await evaluate(`(() => {
        const w = innerWidth;
        const bad = [...document.querySelectorAll('body *')].filter(el => {
          const r = el.getBoundingClientRect();
          return r.width > 0 && (r.right > w + 1 || r.left < -1);
        }).slice(0, 5).map(el => ({ tag: el.tagName, cls: String(el.className).slice(0, 70), right: Math.round(el.getBoundingClientRect().right) }));
        return { scrollWidth: document.documentElement.scrollWidth, innerWidth: w, bad };
      })()`);
      if (!allowOverflow) {
        assert.ok(overflow.scrollWidth <= overflow.innerWidth, 'horizontal overflow on ' + id + ': ' + JSON.stringify(overflow.bad));
      }
      assert.deepEqual(errors, []);
      const suffix = (viewportArg || themeArg || pagesArg) ? '-' + width + 'x' + height + (themeArg ? '-' + themeArg : '') : '';
      const name = page.name.replace(/\.png$/, suffix + '.png');
      const shot = await call('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
      const png = Buffer.from(shot.data, 'base64');
      assert.ok(png.length > 15000);
      fs.writeFileSync(path.join(out, name), png);
      captured.push({ page: id, file: path.relative(root, path.join(out, name)), width, height, theme: themeArg || 'default', bytes: png.length });
    }
    console.log(JSON.stringify({ passed: true, screenshots: captured, synthetic: true, runtimeErrors: errors }));
  } finally {
    if (ws) ws.close();
    if (process.platform === 'win32') spawn('taskkill', ['/pid', String(chrome.pid), '/T', '/F'], { windowsHide: true, stdio: 'ignore' });
    else chrome.kill();
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
