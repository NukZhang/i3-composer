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
  brd: () => request('/api/brd'),
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
  answer: (id, pbc, approve, note) =>
    request('/api/items/' + encodeURIComponent(id) + '/answer' + q(pbc), {
      method: 'POST',
      body: JSON.stringify({ approve, note: note || '' }),
    }),
  createUnit: (body) => request('/api/units', { method: 'POST', body: JSON.stringify(body) }),
  svcVerify: (id, body) =>
    request('/api/units/' + encodeURIComponent(id) + '/svcverify', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  saveBRD: (text, note) => request('/api/brd', { method: 'POST', body: JSON.stringify({ text, note }) }),
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
