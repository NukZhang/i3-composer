// api.js —— 与 BFF 的唯一通道。
//
// 三条硬规则（与 docs/SPEC-frontend-wiring.md 一致）：
//   1. 每个读都打在 /api/*，页面不持有自己的真相；
//   2. 每个写都是一个真实请求，成功后重新拉全量，绝不做本地乐观更新——
//      否则越权时界面已经变了，而账本里什么也没发生；
//   3. 错误只把后端原文抛出来，前端不发明措辞。

export class ApiError extends Error {
  constructor(message, status) {
    super(message);
    this.status = status;
  }
}

async function request(path, opts) {
  const init = { headers: { 'Content-Type': 'application/json' }, ...(opts || {}) };
  const r = await fetch(path, init);
  const txt = await r.text();
  let body = null;
  if (txt) {
    try {
      body = JSON.parse(txt);
    } catch {
      body = null; // 非 JSON：保留 null，错误信息退化成状态码
    }
  }
  if (!r.ok) {
    throw new ApiError((body && body.error) || '请求失败（HTTP ' + r.status + '）', r.status);
  }
  return body;
}

const q = (pbc) => (pbc ? '?pbc=' + encodeURIComponent(pbc) : '');

export const api = {
  health: () => request('/api/health'),

  // 读
  state: (pbc) => request('/api/state' + q(pbc)),
  overview: (pbc) => request('/api/overview' + q(pbc)),
  units: () => request('/api/units'),
  brd: (proj) => request('/api/brd' + (proj ? '?proj=' + encodeURIComponent(proj) : '')),
  // v2.7：工作空间 → 项目
  projects: () => request('/api/projects'),
  createProject: (body) => request('/api/projects', { method: 'POST', body: JSON.stringify(body) }),
  joinProject: (id) => request('/api/projects/join', { method: 'POST', body: JSON.stringify({ id }) }),
  startBRD: (proj) => request('/api/projects/' + encodeURIComponent(proj) + '/brd', { method: 'POST' }),
  createWorkspace: (body) => request('/api/workspaces', { method: 'POST', body: JSON.stringify(body) }),
  joinWorkspace: (id) => request('/api/workspaces/join', { method: 'POST', body: JSON.stringify({ id }) }),
  agents: () => request('/api/agents'),
  agent: (id) => request('/api/agents/' + encodeURIComponent(id)),
  tasks: (pbc, tab) => request('/api/tasks' + q(pbc) + '&tab=' + encodeURIComponent(tab)),
  item: (id, pbc) => request('/api/items/' + encodeURIComponent(id) + q(pbc)),
  facts: (pbc) => request('/api/facts' + q(pbc)),
  notifications: () => request('/api/notifications'),
  inbox: () => request('/api/inbox'),

  // 写（角色来自服务端会话 cookie）
  setRole: (role) => request('/api/role', { method: 'POST', body: JSON.stringify({ role }) }),
  createTask: (pbc, body) => request('/api/tasks' + q(pbc), { method: 'POST', body: JSON.stringify(body) }),
  runItem: (id, pbc) => request('/api/items/' + encodeURIComponent(id) + '/run' + q(pbc), { method: 'POST' }),
  // 准入被拒后补输入：验收口径 / 数据范围 / 责任人。发起时重判。
  admitInputs: (id, body, pbc) =>
    request('/api/items/' + encodeURIComponent(id) + '/inputs' + q(pbc), {
      method: 'POST',
      body: JSON.stringify(body || {}),
    }),
  answer: (id, pbc, approve, note) =>
    request('/api/items/' + encodeURIComponent(id) + '/answer' + q(pbc), {
      method: 'POST',
      body: JSON.stringify({ approve, note: note || '' }),
    }),
  createUnit: (body) => request('/api/units', { method: 'POST', body: JSON.stringify(body) }),
  materialPreview: (id) => request('/api/units/' + encodeURIComponent(id) + '/material-preview', { method: 'POST' }),
  svcVerify: (id, body) =>
    request('/api/units/' + encodeURIComponent(id) + '/svcverify', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  // v3.2：单元打 tag 进「发布编排」池；发布会是项目级批次
  tagUnit: (id) => request('/api/units/' + encodeURIComponent(id) + '/tag', { method: 'POST' }),
  // 只读回放：按 tag 打下去的那一刻重算投影，回答「凭什么能发」。
  replay: (id, tag) =>
    request('/api/units/' + encodeURIComponent(id) + '/replay' + (tag ? '?tag=' + encodeURIComponent(tag) : '')),
  orch: (proj) => request('/api/projects/' + encodeURIComponent(proj) + '/orch'),
  release: (proj, body) =>
    request('/api/projects/' + encodeURIComponent(proj) + '/orch/release', {
      method: 'POST',
      body: JSON.stringify(body || {}),
    }),
  rollback: (proj, id, note) =>
    request('/api/projects/' + encodeURIComponent(proj) + '/orch/rollback', {
      method: 'POST',
      body: JSON.stringify({ id, note: note || '' }),
    }),
  saveBRD: (text, note, proj) => request('/api/brd' + (proj ? '?proj=' + encodeURIComponent(proj) : ''), { method: 'POST', body: JSON.stringify({ text, note }) }),
  agentEval: (id, body) =>
    request('/api/agents/' + encodeURIComponent(id) + '/eval', { method: 'POST', body: JSON.stringify(body) }),
  agentBaseline: (id, runId) =>
    request('/api/agents/' + encodeURIComponent(id) + '/baseline', {
      method: 'POST',
      body: JSON.stringify({ runId }),
    }),
  agentPublish: (id) => request('/api/agents/' + encodeURIComponent(id) + '/publish', { method: 'POST' }),
  agentRollout: (id, pct) =>
    request('/api/agents/' + encodeURIComponent(id) + '/rollout', {
      method: 'POST',
      body: JSON.stringify({ pct }),
    }),
  agentRollback: (id) => request('/api/agents/' + encodeURIComponent(id) + '/rollback', { method: 'POST' }),

  // v2.6：编排工作台。保存编排 → digest 变；构建 → 换版本号；
  // 试跑 → 记录一轮；沉淀 → 进评测集。
  agentFlow: (id, flow) =>
    request('/api/agents/' + encodeURIComponent(id) + '/flow', {
      method: 'PUT',
      body: JSON.stringify({ flow }),
    }),
  agentBuild: (id) => request('/api/agents/' + encodeURIComponent(id) + '/build', { method: 'POST' }),
  agentTrial: (id, body) =>
    request('/api/agents/' + encodeURIComponent(id) + '/trial', {
      method: 'POST',
      body: JSON.stringify(body || {}),
    }),
  agentCase: (id, cse) =>
    request('/api/agents/' + encodeURIComponent(id) + '/cases', {
      method: 'POST',
      body: JSON.stringify(cse || {}),
    }),
  kbDispatch: (id, idx) =>
    request('/api/agents/' + encodeURIComponent(id) + '/kb/' + idx + '/dispatch', { method: 'POST' }),

  // v2.5：真人验收台
  checkDecide: (id, body, pbc) =>
    request('/api/checks/' + encodeURIComponent(id) + '/decide' + q(pbc), {
      method: 'POST',
      body: JSON.stringify(body || {}),
    }),
  verifyChat: (unit, text) =>
    request('/api/units/' + encodeURIComponent(unit) + '/chat', {
      method: 'POST',
      body: JSON.stringify({ text }),
    }),

  addAnnotation: (body) => request('/api/annotations', { method: 'POST', body: JSON.stringify(body) }),
  saveDraft: (pbc, body) =>
    request('/api/artifacts' + q(pbc), { method: 'POST', body: JSON.stringify(body) }),

  // SSE：只通知「有变化了」，不携带账本内容。
  stream(pbc, onChange) {
    const es = new EventSource('/api/timeline/stream' + q(pbc));
    let timer = null;
    es.addEventListener('change', () => {
      // 一次交互可能连着落好几条事实：合并成一次重拉即可。
      clearTimeout(timer);
      timer = setTimeout(onChange, 120);
    });
    // EventSource 自带重连；这里不自己造重连风暴。
    return () => es.close();
  },
};
