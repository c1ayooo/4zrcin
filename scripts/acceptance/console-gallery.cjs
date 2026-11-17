// Reproducible README screenshots from synthetic responses and the real bundle.
// No console, harness, saved project, model API or credential is accessed.
const fs = require('node:fs');
const path = require('node:path');
const { spawn } = require('node:child_process');
const assert = require('node:assert/strict');
const root = path.resolve(__dirname, '../..');
const web = path.join(root, 'internal/web/static');
const out = path.join(root, '.build/ui-qa');
fs.mkdirSync(out, { recursive: true });
const profileDir = fs.mkdtempSync(path.join(out, 'gallery-chrome-'));
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
function api(url) {
  const route = url.pathname.split('/api/')[1];
  switch (route) {
    case 'projects': return { projects: [project, { ...project, id: 'fixture-archive', name: '交接归档示例',
      running: 0, intents: 8, facts: 16, evidence: 24 }], max_runs: 3, round_timeout_seconds: 900 };
    case 'settings': return { configured: true, model: 'example-model', api_key_set: true,
      provider:'openai', base_url:'https://gateway.example.test/v1', context_window:128000, path: '~/.cyber/cyber.yaml' };
    case 'workers': return { workers, count: workers.length };
    case 'peers': return { mode: 'open', peer_policy: { mode: 'open', allow: [] } };
    case 'participants': return { participants: workers.map(w => ({ id: w.participant_id, title: w.title, intent_id: w.intent_id })) };
    case 'nodes': return { nodes: [], default: 'local' };
    case 'runtime-settings': return { auxiliary_concurrency: 1 };
    case 'board': return { generated_at: at, case: 'fixture', project_paused: true,
      intents: { total: 3, running: 1, waiting: 1, done: 1 },
      facts: { total: 6, verified: 4, by_kind: {}, by_origin: {} }, evidence: 4,
      runs: { total: 3, running: 1, done: 2 }, goals: { total: 1, met: 0, open: [] },
      decider: { enabled: true, paused: true, paused_reason: '操作员暂停' },
      open_intents: [], waiting_intents: [], running_intents: [], recent_facts: [], frontier: [], handoff_warnings: [] };
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
    for (const [route, expected, name] of [
      ['/g/?view=settings', '统一模型连接', 'models.png'],
      ['/p/fixture/?view=runs&tab=workers', '核对账户访问边界', 'workers.png'],
      ['/g/?view=projects', '应用安全评估示例', 'projects.png'],
    ]) {
      await call('Emulation.setDeviceMetricsOverride', { width: 1440, height: name === 'projects.png' ? 640 : 1000, deviceScaleFactor: 1, mobile: false });
      await call('Page.navigate', { url: 'http://tianwu.test' + route });
      let ready = false;
      for (let i = 0; i < 100 && !ready; i++) {
        ready = await evaluate('document.body.innerText.includes(' + JSON.stringify(expected) + ')');
        if (!ready) await pause(75);
      }
      assert.ok(ready, route + ' did not render');
      await pause(250);
      if (name === 'workers.png') assert.ok(await evaluate('!document.body.innerText.includes("造人") && !document.body.innerText.includes("造一个人")'));
      assert.ok(await evaluate('document.documentElement.scrollWidth <= innerWidth'), 'horizontal overflow');
      assert.deepEqual(errors, []);
      const shot = await call('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
      const png = Buffer.from(shot.data, 'base64');
      assert.ok(png.length > 15000);
      fs.writeFileSync(path.join(out, name), png);
    }
    console.log(JSON.stringify({ passed: true, screenshots: ['models.png', 'workers.png', 'projects.png'], synthetic: true, runtimeErrors: errors }));
  } finally {
    if (ws) ws.close();
    if (process.platform === 'win32') spawn('taskkill', ['/pid', String(chrome.pid), '/T', '/F'], { windowsHide: true, stdio: 'ignore' });
    else chrome.kill();
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
