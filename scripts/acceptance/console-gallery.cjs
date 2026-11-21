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
// --hub swaps the graph fixture for a synthetic hub snapshot: the project brief
// is the premise of every root direction, so its chain closure is the whole map.
// That is the case that must open on the main path instead of lighting 200 of
// 301 nodes. Offline sample data only.
const hubArg = argv.includes('--hub');
// --expand opens every <details> before the shot (tool failure reasons, legend).
const expandArg = argv.includes('--expand');
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
// --pending swaps the empty operator inbox for one with rows and one unreadable
// category, so a capture can show the badge's lower bound ("4+"), the card rows
// and the click-through to a record. Synthetic only.
const pendingArg = argv.includes('--pending');
// --probe <selector> prints one element's innerText after render (debugging aid).
const probeArg = argValue('probe');
// --click <selector> clicks one element after the page is ready (opening a
// report preview or a disclosure) so the shot shows the expanded state.
const clickArg = argValue('click');
// --views opens the graph's 视图 popover before the shot (layout names + help).
const viewsArg = argv.includes('--views');
// --eval <expression> prints the JSON value of one page expression (debugging aid).
const probeJS = argValue('eval');
const [viewportWidth, viewportHeight] = (viewportArg || '1440x1000').split('x').map(Number);
if (!Number.isFinite(viewportWidth) || !Number.isFinite(viewportHeight)) throw Error('bad --viewport: ' + viewportArg);
if (themeArg && themeArg !== 'dark' && themeArg !== 'light') throw Error('bad --theme: ' + themeArg);

// Page table used by both the original gallery and the parameterized runs.
const PAGES = {
  fleet: { route: '/g/?view=fleet', expected: '运行时间线', name: 'fleet.png', height: 900 },
  settings: { route: '/g/?view=settings', expected: '统一模型连接', name: 'models.png', height: 1000 },
  workers: { route: '/p/fixture/?view=runs&tab=workers', expected: '核对账户访问边界', name: 'workers.png', height: 1000 },
  projects: { route: '/g/?view=projects', expected: '应用安全评估示例', name: 'projects.png', height: 640 },
  overview: { route: '/p/fixture/?view=overview', expected: '失败运行', name: 'overview.png', height: 900 },
  state: { route: '/p/fixture/?view=state', expected: '核对账户访问边界', name: 'state.png', height: 900 },
  runs: { route: '/p/fixture/?view=runs', expected: '事件流', name: 'runs.png', height: 900 },
  messages: { route: '/p/fixture/?view=messages', expected: '事实质量修正已完成', name: 'messages.png', height: 900 },
  graph: { route: '/p/fixture/?view=graph#f:f_a', expected: '事实', name: 'graph-node.png', height: 900 },
  'graph-map': { route: '/p/fixture/?view=graph', expected: '快照已载', name: 'graph-map.png', height: 900 },
  // The brief itself: with --hub its closure is the whole synthetic map, which is
  // the default-main-path case.
  'graph-brief': { route: '/p/fixture/?view=graph#p:fixture', expected: 'portal.example.test（离线示例）', name: 'graph-brief.png', height: 900 },
  // Tool statistics: twelve synthetic rows, so the capped list and the sort
  // controls are both exercised.
  toolbox: { route: '/p/fixture/?view=runtime&tab=toolbox', expected: '工具与能力', name: 'toolbox.png', height: 1000 },
  // Record-level inbox links: an authorization request on 访问 (`?id=`) and a
  // stale goal receipt on 状态 (`?goal=`). Both need --pending for the inbox data.
  'access-auth': { route: '/p/fixture/?view=access&id=req_fixture_1', expected: '授权请求', name: 'access-auth.png', height: 900 },
  // The access page itself: recent closed requests whose two states disagree.
  'access-recent': { route: '/p/fixture/?view=access', expected: '最近处理过的请求', name: 'access-recent.png', height: 900 },
  'state-goal': { route: '/p/fixture/?view=state&goal=g_access', expected: '目标验收回执', name: 'state-goal.png', height: 900 },
  // One direction whose premise is unavailable: 等待原因 must name the premise
  // ids, not hide them in a tooltip.
  'state-blocked': { route: '/p/fixture/?view=state&intent=in_review', expected: '复核访问控制结论', name: 'state-blocked.png', height: 1000 },
  // The report trace: 预检 lists the facts no report cites, 结论行 carries its fact
  // id, and a fact has a record address of its own (`?view=graph&fact=`).
  reports: { route: '/p/fixture/?view=reports', expected: '报告列表', name: 'reports.png', height: 1000 },
  // One open report as a record: the trace's way back lands on this URL.
  'report-open': { route: '/p/fixture/?view=reports&report=rep_fixture_1', expected: '报告列表', name: 'report-open.png', height: 1000 },
  fact: { route: '/p/fixture/?view=graph&fact=f_a', expected: '断言与复核', name: 'fact-trace.png', height: 900 },
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
    description: '复核结论是否成立。', node: 'local', notes: [], from_fact_ids: ['f_premise'] },
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

// Facts a direction declared, as GET /api/intents/{id} returns them. Three
// statuses on purpose (verified / unverified / withdrawn) so the detail's
// counters and row labels are visible in one screenshot. Offline sample data.
const fixtureIntentFacts = {
  in_access: [
    { id: 'f_acc_1', intent_id: 'in_access', kind: 'vuln', title: '示例：账户越权读取他人订单',
      summary: '只读示例，不含真实数据。', confidence: 'observed', status: 'verified',
      severity: 'high', proof: 'dynamic', claim_version: 3, created_at: at,
      evidence_ids: ['e_acc_1', 'e_acc_2'], refs: [], tags: [] },
    { id: 'f_acc_2', intent_id: 'in_access', kind: 'host', title: '示例：边界主机响应带审计头',
      summary: '只读示例。', confidence: 'inferred', status: 'unverified', proof: 'dynamic',
      claim_version: 1, created_at: at, evidence_ids: ['e_acc_3'], refs: [], tags: [] },
    { id: 'f_acc_3', intent_id: 'in_access', kind: 'cred', title: '示例：只读账户凭据（已撤回）',
      summary: '凭据已由写入方撤回。', confidence: 'observed', status: 'withdrawn',
      claim_version: 1, created_at: at, evidence_ids: [], refs: [], tags: [] },
  ],
  // A pending direction that cites a premise declared by another direction: the
  // detail says the premise is missing from this response instead of assuming it
  // passed. That is the shape the ledger really has for a blocked direction.
  in_review: [],
};

// Reports and their precheck. The precheck deliberately carries all three lists:
// facts no delivered report cites, one report whose rows cannot be read, and one
// that failed its own checks — the console must name the last two, not fold them
// into a zero. Offline sample data.
const fixtureReports = [
  { id: 'rep_fixture_1', status: 'ready', created_at: at, finished_at: at,
    source_digest: 'sha256:fixture', findings: 3, run_id: 'r_report',
    checks: [
      { rule: 'fact_exists', ok: true, detail: '三条结论都能在账本里找到' },
      { rule: 'evidence_present', ok: true, detail: '每条结论都有证据行' },
    ] },
  { id: 'rep_fixture_2', status: 'stale', created_at: at, finished_at: at,
    source_digest: 'sha256:older', findings: 2,
    checks: [{ rule: 'ledger_snapshot', ok: false, detail: '账本在生成后发生变化' }] },
];
const fixtureReportFindings = [
  { fact: 'f_a', verdict: 'confirmed', title: '示例：网关 OPTIONS 绕过鉴权', severity: 'medium' },
  { fact: 'f_b', verdict: 'confirmed', title: '示例：网关白名单直通下游 actuator', severity: 'high' },
  { fact: 'f_d', verdict: 'candidate', title: '示例：管理端导出接口缺少角色校验', severity: 'high' },
];
const fixturePrecheck = {
  reportable: 5, unverified: 1, rejected: 0, withdrawn: 1, running: false,
  unreported: {
    total: 23, limit: 20,
    items: [
      { fact: 'f_c', title: '示例：登录验证码可绕过' },
      { fact: 'f_e', title: '示例：对象存储列举权限外露' },
      { fact: 'f_g', title: '示例：预发环境调试端口可从办公网访问' },
    ],
    note: '只按已通过校验（status=ready）报告的 findings.json 里的 fact id 算差：命中即算已入，不判断该报告是否已过期。',
    unreadable: ['rep_old_1'],
    uncounted: ['rep_old_2'],
  },
};

// A pending inbox for the --pending shot: two handoffs, one authorization ask,
// one run warning, and tool approval unreadable. `countable` is false because
// one category is unknown, so the badge must show a lower bound instead of an
// exact number. Offline sample data only; the ids are synthetic.
const fixtureInboxPending = {
  // Sample B shape from the ledger: the control plane still says pending while
  // the body the commander wrote opens with a delivered-report marker.
  authorization: [{ id: 'req_fixture_1', status: 'pending', asset: 'portal.example.test（离线示例）',
    impact: '只影响示例环境的报告页',
    action: '【报告已交付（20:50）】报告已挂到项目报告页，等待复核',
    created_at: at, updated_at: at }],
  handoff: [
    { intent_id: 'in_evidence', title: '整理本轮原始证据', status: 'waiting',
      request: { id: 'ow_fixture_1', request: '需要你确认证据目录的保留期' } },
    { intent_id: 'in_entry', title: '确认示例环境入口', status: 'waiting',
      request: { id: 'ow_fixture_2', request: '入口清单里有两个地址无法连通，是否跳过' } },
  ],
  acceptance: [{ goal_id: 'g_access', statement: '核对边界外主机的访问是否受限', receipt_changed: ['f_2'] }],
  warnings: [{ run: 'r_verify', intent_id: 'in_review', warnings: ['示例告警：交接缺少验收条目'] }],
  guardrail: { state: 'unavailable', reason: '没有可用会话' },
  // Closed requests, read-only and outside the todo counts. Both carry the two
  // states that disagree, so the access page has to show both sides.
  recent: [
    { id: 'req_fixture_old_1', status: 'dismissed', resolution: 'work_resumed',
      resolved_at: at, created_at: at,
      action: '把测试范围扩到 10.0.0.0/24（示例）', asset: '10.0.0.0/24（示例）' },
    { id: 'req_fixture_old_2', status: 'dismissed', resolution: 'work_resumed',
      resolved_at: at, created_at: at,
      action: '对示例网关做并发扫描', asset: 'gateway.example.test（示例）' },
  ],
  total: 5,
  summary: {
    countable: false,
    total: 5,
    categories: [
      { kind: 'authorization', state: 'ok', count: 1, items: [{ kind: 'authorization', id: 'req_fixture_1' }] },
      { kind: 'handoff', state: 'ok', count: 2, items: [
        { kind: 'handoff', id: 'in_evidence', title: '整理本轮原始证据' },
        { kind: 'handoff', id: 'in_entry', title: '确认示例环境入口' },
      ] },
      { kind: 'acceptance', state: 'ok', count: 1, items: [{ kind: 'acceptance', id: 'g_access' }] },
      { kind: 'warning', state: 'ok', count: 1, items: [{ kind: 'warning', id: 'r_verify' }] },
      { kind: 'guardrail', state: 'unavailable', reason: '没有可用会话', count: 0, items: [] },
    ],
  },
};

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

const hubIntents = Array.from({ length: 60 }, (_, i) => ({
  id: `in_h${i}`, title: `示例方向 ${i + 1}`, status: 'done', attempts: 1,
  created_at: `2026-11-16T09:${String(i).padStart(2, '0')}:00+08:00`,
  updated_at: `2026-11-16T09:${String(i).padStart(2, '0')}:30+08:00`, node: 'local', notes: [],
}));
const hubFacts = hubIntents.flatMap((it, i) => [0, 1, 2, 3].map((j) => fixtureFactOf(
  `f_h${i}_${j}`, `示例产出 ${i + 1}-${j + 1}`, 'low',
  `2026-11-16T09:${String(i).padStart(2, '0')}:${String(10 + j * 5).padStart(2, '0')}+08:00`,
)));
hubFacts.forEach((f, i) => { f.intent_id = hubIntents[Math.floor(i / 4)].id; });
const fixtureHubGraph = {
  ...fixtureGraph,
  facts: hubFacts,
  intents: hubIntents,
  edges: [{ id: 'e_hub', source: 'f:f_h0_0', target: 'f:f_h1_0', kind: 'reference' }],
  fact_meta: hubFacts.map((f) => ({ id: f.id, needs_review: false, reviewed_claim_version: 1, evidence_count: 0, ref_count: 0 })),
  total: hubFacts.length + hubIntents.length + 1,
  facts_total: hubFacts.length, intents_total: hubIntents.length, goals_total: 0, edges_total: 1, boundary_total: 0,
};

// Tool page fixtures: twelve rows so the visible cap is exercised, with rates
// and durations spanning "good", "flaky", "slow" and "no result yet". All offline.
const fixtureToolRanks = [
  { tool: 'bash', calls: 412, runs: 38, completed: 400, failures: 12, duration_ms: 96_000, last_at: '2026-11-16T09:58:00+08:00',
    error_kinds: [{ label: 'exit code 1', count: 7 }, { label: 'no such file or directory', count: 3 }, { label: 'timeout', count: 2 }] },
  { tool: 'local:curl', local: true, calls: 166, runs: 24, completed: 160, failures: 9, duration_ms: 240_000, last_at: '2026-11-16T09:55:00+08:00',
    error_kinds: [{ label: 'unsupported flag --tlsv1.2', count: 5 }, { label: 'exit code 23', count: 4 }] },
  { tool: 'read_file', calls: 121, runs: 30, completed: 121, failures: 0, duration_ms: 8_000, last_at: '2026-11-16T09:57:00+08:00' },
  { tool: 'gogo', calls: 74, runs: 12, completed: 70, failures: 26, duration_ms: 1_120_000, last_at: '2026-11-16T09:40:00+08:00' },
  { tool: 'local:nuclei', local: true, calls: 58, runs: 9, completed: 58, failures: 2, duration_ms: 2_400_000, last_at: '2026-11-16T09:31:00+08:00' },
  { tool: 'write_file', calls: 44, runs: 18, completed: 44, failures: 0, duration_ms: 3_000, last_at: '2026-11-16T09:52:00+08:00' },
  { tool: 'rg', calls: 39, runs: 15, completed: 39, failures: 1, duration_ms: 6_500, last_at: '2026-11-16T09:47:00+08:00' },
  { tool: 'python', calls: 21, runs: 7, completed: 20, failures: 4, duration_ms: 88_000, last_at: '2026-11-16T09:22:00+08:00' },
  { tool: 'tokenize', calls: 8, runs: 3, completed: 0, failures: 0, duration_ms: 0, last_at: '2026-11-16T09:12:00+08:00' },
  { tool: 'local:spray', local: true, calls: 6, runs: 2, completed: 6, failures: 3, duration_ms: 30_000, last_at: '2026-11-16T09:05:00+08:00' },
  { tool: 'katana', calls: 3, runs: 1, completed: 3, failures: 0, duration_ms: 210_000, last_at: '2026-11-16T08:58:00+08:00' },
  { tool: 'local:neutron', local: true, calls: 1, runs: 1, completed: 1, failures: 1, duration_ms: 2_000, last_at: '2026-11-16T08:51:00+08:00' },
];
const fixtureArsenal = [
  { name: 'nuclei', version: 'v3.3.5', size: 41_000_000, source: 'download', managed_by: 'arsenal', dirs: ['C:/example/arsenal/bin/nuclei.exe'] },
  { name: 'gogo', version: 'v2.11.0', size: 18_400_000, source: 'bundle', managed_by: 'arsenal', dirs: ['C:/example/arsenal/bin/gogo.exe'] },
  { name: 'katana', version: 'v1.1.0', size: 22_000_000, source: 'download', managed_by: 'arsenal', dirs: ['C:/example/arsenal/bin/katana.exe'] },
];
const fixtureToolRoots = [
  { path: 'C:/example/arsenal', bin: 'C:/example/arsenal/bin', source: 'instance', exists: true, resolved: true, path_injected: true, note: '实例目录；当前宿主已从 PATH 解析到这里的工具。' },
  { path: 'C:/Users/example/.arsenal', bin: 'C:/Users/example/.arsenal/bin', source: 'user', exists: false, note: '用户目录；尚未创建，装到这里的工具不会出现在上面的清单里。' },
];
const fixtureRuntimeTools = {
  state: 'connected', agent_id: 'host-fixture', provider: 'example-provider', model: 'example-model',
  checked_at: at,
  commands: [
    { name: '!bash', description: '在宿主上执行命令（内嵌 POSIX 解释器）', usage: '!bash -lc "cmd"' },
    { name: '!read_file', description: '按行读取文本文件', usage: '!read_file path' },
    { name: '!write_file', description: '写入文件，自动创建父目录', usage: '!write_file path content' },
    { name: '/skill:http-probe', description: '按技能探测 HTTP 面', usage: '/skill:http-probe URL' },
    { name: '/compact', description: '压缩会话上下文' },
    { name: '!curl', description: '纯 Go 复刻的 curl', usage: '!curl -sS -o out.json URL' },
  ],
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
  // The report trace: the precheck answer, the report list, and the two files a
  // report delivers (findings.json is JSON, index.md is plain text).
  if (route === 'reports/precheck') return fixturePrecheck;
  if (route === 'reports') return { reports: fixtureReports };
  if (route && /^reports\/[^/]+\/file$/.test(route)) {
    const path = url.searchParams.get('path') || '';
    if (path === 'findings.json') return fixtureReportFindings;
    if (path === 'index.md') return { __raw: '# 应用安全评估示例（离线合成）\n\n本报告用于控制台截图，不含真实项目、凭据或运行材料。\n' };
    return { __raw: '' };
  }
  // One direction's own facts (GET /api/intents/{id}); the detail page reads
  // status and evidence from this ledger query, not from the case snapshot.
  if (route && /^intents\/[^/]+$/.test(route)) {
    const id = decodeURIComponent(route.slice('intents/'.length))
    const intent = fixtureIntents.find((row) => row.id === id)
    assert.ok(intent, 'unknown intent fixture: ' + id)
    const facts = fixtureIntentFacts[id] ?? []
    return { intent, facts, facts_total: facts.length, facts_has_more: false }
  }
  switch (route) {
    case 'fleet': {
      const run = { ...fixtureRuns[0], title: fixtureIntents[0].title };
      const active = {
        id: project.id, name: project.name, scope: project.scope,
        decider: { enabled: true, rounds: 3, activity: { phase: 'model', since: recent, detail: '正在核对项目状态' } },
        intents: { total: 4, pending: 1, claimed: 0, running: 1, done: 1, failed: 0, parked: 0 },
        facts: { total: 6, with_evidence: 4, verified: 4 }, evidence: 4,
        goals: { total: 2, met: 1 }, runs: { total: 1, running: 1, done: 0, failed: 0, canceled: 0 },
        running: [{ ...fixtureIntents[0], created_at: recent, run_id: run.id }],
        open: [{ ...fixtureIntents[3], created_at: recent }], recent_runs: [run], tokens: run.tokens,
      };
      const archive = { ...active, id: 'fixture-archive', name: '交接归档示例', decider: { enabled: true, paused: true, rounds: 2 },
        intents: { ...active.intents, pending: 0, running: 0 }, goals: { total: 1, met: 1 },
        running: [], open: [], recent_runs: [], tokens: 920 };
      return { generated_at: recent, projects: [active, archive],
        nodes: [{ id: 'local', running: 1, health: { online: true, checked_at: recent, latency_ms: 3 } }] };
    }
    case 'intents': return { intents: fixtureIntents, total: fixtureIntents.length, has_more: false };
    case 'runs': return { runs: fixtureRuns };
    case 'goals': return { goals: [
      // A met goal whose cited facts changed after the seal: the case the
      // inbox's 失效的验收回执 row links to (`?view=state&goal=g_access`).
      { id: 'g_access', statement: '核对边界外主机的访问是否受限', met: true, created_at: at,
        start: '已有授权清单与测试窗口', note: '示例：以只读方式确认了边界，未执行写操作',
        from: ['f_1', 'f_2'], completed_at: at,
        receipt_stale: true, receipt_changed: ['f_2'] },
      { id: 'g_evidence', statement: '每一条结论都有可复核证据', met: true, created_at: at },
    ] };
    case 'access': return { access: [] };
    case 'usage/breakdown': return {
      commander: { total: 3200, runs: 4 }, executor: { total: 5100, runs: 9 },
      executor_total: { total: 5100 }, judge: { jev: { total: 400 }, llm: { total: 120 } },
      combined: { total: 8300 }, all: { total: 8820 },
    };
    case 'operator-inbox': return pendingArg ? fixtureInboxPending : {
      authorization: [], handoff: [], acceptance: [], warnings: [], total: 0,
      summary: {
        countable: true, total: 0,
        categories: ['authorization', 'handoff', 'acceptance', 'warning', 'guardrail']
          .map(kind => ({ kind, state: 'none', count: 0, items: [] })),
      },
    };
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
    case 'graph': return hubArg ? fixtureHubGraph : fixtureGraph;
    case 'tools': return {
      leaderboard: fixtureToolRanks, runs_scanned: 42, arsenal: fixtureArsenal,
      arsenal_dirs: ['C:/example/arsenal', 'C:/Users/example/.arsenal'],
      tool_roots: fixtureToolRoots,
      tool_roots_note: '根目录由实例自身的数据目录规则解析；目录存在不代表已进入当前宿主环境。',
    };
    case 'tools/runtime': return fixtureRuntimeTools;
    case 'changes': return { changes: [], has_more: false, next_cursor: '', high_watermark: { epoch: 'e_fixture', seq: 0 }, pending: 0 };
    // A fact detail: claim version + the review bound to it + its evidence rows.
    // The panel derives "已复核 / 旧断言" from these, so the fixture keeps the shape
    // of a reviewed claim rather than a bare fact row.
    case 'facts/f_a': return {
      fact: { ...fixtureFact, claim_version: 1, evidence_ids: ['e_fixture_1'] },
      version: 1,
      // One original evidence row: the trace's third hop must have something to
      // open, otherwise the screenshot stops at "证据 (0)".
      evidence: [{
        id: 'e_fixture_1', run_id: 'r_access', intent_id: 'in_gateway',
        name: 'response-sample.txt', mime: 'text/plain', size: 168,
        uri: 'evidence/response-sample.txt', created_at: at,
      }],
      reviews: [{
        id: 'rv_fixture_1', fact_id: 'f_a', verdict: 'pass', claim_version: 1,
        claim_hash: 'sha256:fixture-claim', verifier_run: 'r_verify_fixture',
        notes: '示例复核：重跑并核对了响应样本', created_at: at,
      }],
    };
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
      // The operator action contract (internal/api/project_actions.go). Manual
      // pause: 继续 is available, so the banner must not claim 已收口.
      project_actions: {
        state: { paused: true, kind: 'manual_pause', reason: '操作员暂停' },
        facts: { has_ledger: true, goals_total: 2, goals_met: 1, goals_open: 1, goals_active: 2,
          pending_directions: 1, unread_messages: 0 },
        actions: [
          { id: 'pause', enabled: false, reason_code: 'already_paused', reason: '项目已暂停' },
          { id: 'resume', enabled: true, reason_code: 'paused_manual', reason: '人工暂停：继续会重新派发待办方向' },
          { id: 'append_goal', enabled: true, reason_code: 'ok', reason: '追加一个新目标并继续（不改动已达成目标的完成收据）' },
        ],
        notes: [],
      },
      open_intents: [], running_intents: [],
      waiting_intents: [{ id: 'in_evidence', title: '整理本轮原始证据', status: 'waiting' }],
      recent_facts: [
        { id: 'f_1', title: '示例入口可访问', kind: 'observation', status: 'verified', evidence: 2, origin: 'executor' },
        { id: 'f_2', title: '示例账户缺少边界校验', kind: 'observation', status: 'unverified', evidence: 1, origin: 'executor' },
        { id: 'f_3', title: '示例响应带审计头', kind: 'observation', status: 'unverified', evidence: 0, origin: 'executor' },
      ],
      frontier: [
        // The board appends the reason to a blocked premise id (`blocked_on` in
        // internal/board/board.go), and the detail has to split id from reason.
        { id: 'in_review', title: '复核访问控制结论', ready: false, blocked_on: ['f_premise（已撤回）'],
          premise_changed: ['f_acc_1'], last_declared: 0 },
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
    let port = 0;
    for (let i = 0; i < 100 && !port; i++) {
      try {
        port = Number(fs.readFileSync(portFile, 'utf8').split('\n')[0]);
      } catch (error) {
        if (error.code !== 'ENOENT' && error.code !== 'EBUSY') throw error;
      }
      if (!port) await pause(100);
    }
    assert.ok(port > 0, 'Chrome did not publish its DevTools port');
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
          const payload = api(url);
          if (payload && typeof payload.__raw === 'string') {
            body = Buffer.from(payload.__raw);
            type = 'text/plain; charset=utf-8';
          } else {
            body = Buffer.from(JSON.stringify(payload));
          }
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
    async function checkDisclosure(label, container, breakpoint) {
      const get = `(() => {
        const button = [...document.querySelectorAll('button[aria-controls][aria-expanded]')]
          .find(node => node.textContent.trim().startsWith(${JSON.stringify(label)}));
        const body = button && document.getElementById(button.getAttribute('aria-controls'));
        const area = document.querySelector(${JSON.stringify(container)});
        return {button, body, area};
      })()`;
      const current = () => evaluate(`(() => {
        const {button, body, area} = ${get};
        return {
          shown: Boolean(button?.getClientRects().length),
          expanded: button?.getAttribute('aria-expanded'),
          content: Boolean(body?.getClientRects().length),
          narrow: area?.clientWidth < ${breakpoint},
        };
      })()`);
      const initial = await current();
      assert.deepEqual(initial, {shown: initial.narrow, expanded: 'false', content: !initial.narrow, narrow: initial.narrow}, label);
      if (!initial.narrow) return;
      await evaluate(`${get}.button.click()`);
      assert.deepEqual(await current(), {shown: true, expanded: 'true', content: true, narrow: true}, label + ' opens');
      await evaluate(`${get}.button.click()`);
      assert.deepEqual(await current(), initial, label + ' closes');
    }
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
      // Optional interaction before the shot (open a report preview / a details
      // block). Asserted, so a renamed control fails the run instead of quietly
      // capturing the collapsed page.
      if (clickArg) {
        const clicked = await evaluate(`(() => {
          const el = document.querySelector(${JSON.stringify(clickArg)});
          if (!el) return false;
          el.click();
          return true;
        })()`);
        assert.ok(clicked, 'click target not found: ' + clickArg);
        await pause(300);
      }
      // Debugging aid: print one element's text after render (no screenshot).
      if (probeArg) {
        const text = await evaluate(`document.querySelector(${JSON.stringify(probeArg)})?.innerText ?? ''`);
        console.log('PROBE ' + JSON.stringify(text));
      }
      if (probeJS) console.log('EVAL ' + JSON.stringify(await evaluate(probeJS)));
      if (viewsArg) {
        await evaluate(`document.querySelector('[aria-label="视图"]')?.click()`);
        await pause(200);
      }
      if (id === 'messages') {
        await checkDisclosure('参与者与积压', 'main > .\\@container', 768);
        assert.ok(await evaluate(`document.querySelector('[aria-label="对话记录"]').clientHeight >= 144`), 'conversation has a readable log');
      }
      if (id === 'runs') {
        await checkDisclosure('运行列表', 'main > .\\@container', 768);
        assert.ok(await evaluate(`(() => {
          const viewport = document.querySelector('.tw-run-stream [data-radix-scroll-area-viewport]');
          return viewport && viewport.clientWidth > 0 && viewport.scrollWidth <= viewport.clientWidth;
        })()`), 'run events fit their scroll pane');
      }
      if (id === 'state') await checkDisclosure('方向列表', 'main > .\\@container', 768);
      if (id === 'graph-map') {
        await checkDisclosure('筛选与检索', '.tw-graph-stage', 640);
        assert.ok(await evaluate(`document.querySelector('.tw-graph-filter').getBoundingClientRect().right <= document.querySelector('.tw-graph-toolbar').getBoundingClientRect().left`), 'graph search and view controls do not overlap');
      }
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
      if (expandArg) {
        await evaluate(`(() => {
          for (const el of document.querySelectorAll('details')) el.open = true;
          return true;
        })()`);
        await pause(200);
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
