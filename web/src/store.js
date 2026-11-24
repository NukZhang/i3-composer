// store.js —— 一个极小的响应式 store。
//
// 为什么不用 Pinia：这个应用只有一个全局状态（当前投影 + 视图状态），
// 引入一个状态库的收益抵不过「多一层间接」的成本。这里只用 Vue 的
// reactive + 一个 refresh 串行器，语义和 v2.1 的 adapter 完全一致。

import { reactive } from 'vue';
import { api } from './api.js';

export const S = reactive({
  // 视图
  view: 'home',        // home | projects | brd | plan | proto | build | verify | release | agent | workers | item | notify | tech | config
  pbcId: 'fee',
  agentId: '',
  itemId: '',
  selCommit: '',
  role: 'BA',
  roleName: '',
  acts: [],
  homeTab: 'todo',
  planTab: 'edit',
  editArt: 'prd',
  protoMode: 'live',
  bpTab: '',
  agTab: '0',
  agRoll: 0,
  evView: 'full',
  evRow: null,
  rpTab: 'rt',
  rpOpen: true,
  annoOn: false,
  loading: false,
  newTask: '',

  // v2.5 真人验收台：当前验收项、bug 表单是否展开。
  vIdx: 0,
  vBugOpen: false,
  // v2.6 编排工作台：选中的节点、是否跑过一条。
  agNode: null,
  agRan: false,

  // 投影（全部来自 /api/*，不持有第二份真相）
  state: null,      // /api/state
  units: [],        // /api/units
  brd: null,        // /api/brd
  agents: [],       // /api/agents
  facts: [],        // /api/facts
  notifications: [],// /api/notifications
  inbox: [],        // /api/inbox

  // 纯展示态（不进账本）
  toasts: [],
  runtime: [],      // 右栏运行时日志
});

let seq = 0;
export function toast(text, kind) {
  const id = ++seq;
  S.toasts.push({ id, text, kind: kind || '' });
  setTimeout(() => {
    const i = S.toasts.findIndex((t) => t.id === id);
    if (i >= 0) S.toasts.splice(i, 1);
  }, 2600);
}

const RUNTIME_KIND = {
  'item.run.started': 'WF',
  'item.step.done': 'ACTOR',
  'item.waiting': 'CONF',
  'item.published': 'PUB',
  'publish.decided': 'CONF',
  'item.blocked': 'CONF',
  'tag.created': 'PUB',
  'deploy.succeeded': 'BIND',
  'annotation.added': 'PUB',
  'agent.eval.run': 'ACTOR',
  'agent.published': 'PUB',
  'agent.rollout': 'PUB',
  'agent.rollback': 'PUB',
  'svc.verified': 'ACTOR',
  'unit.created': 'STATE',
};

export function pushRuntime(type, text) {
  S.runtime.unshift({ at: nowHM(), k: RUNTIME_KIND[type] || 'OTEL', x: text, type });
  if (S.runtime.length > 200) S.runtime.pop();
}

function nowHM() {
  const d = new Date();
  return String(d.getHours()).padStart(2, '0') + ':' + String(d.getMinutes()).padStart(2, '0');
}

// 单个 PBC 的投影 + 单元列表 + 通知，按需拉。
async function loadPbc() {
  const [st, ov] = await Promise.all([api.state(S.pbcId), api.overview(S.pbcId)]);
  S.state = st;
  S.inbox = ov.items;
  S.role = ov.role;
  S.roleName = ov.name;
  S.acts = ov.acts || [];
  if (!S.selCommit || !(st.commits || []).some((c) => c.no === S.selCommit)) {
    S.selCommit = st.commits && st.commits.length ? st.commits[0].no : '';
  }
}

async function loadGlobal() {
  const [units, agents, brd, notifications] = await Promise.all([
    api.units(),
    api.agents(),
    api.brd(),
    api.notifications(),
  ]);
  S.units = units.units || [];
  S.agents = agents.agents || [];
  S.brd = brd;
  S.notifications = notifications.notifications || [];
  // 侧栏的单元 badge 用列表里的 head；但顶栏「Agent 工厂」要的是全部 worker。
  if (S.pbcId && !S.units.some((u) => u.id === S.pbcId)) {
    S.pbcId = S.units.length ? S.units[0].id : 'fee';
  }
}

let inflight = null;
let pending = false;
let lastError = '';

// refresh 是唯一的同步入口：所有写操作成功后都调它。
export function refresh() {
  if (inflight) {
    pending = true;
    return inflight;
  }
  inflight = (async () => {
    S.loading = true;
    try {
      await loadGlobal();
      await loadPbc();
      S.facts = (await api.facts(S.pbcId)).facts || [];
      lastError = '';
    } catch (e) {
      // 拉不到就保留上一份投影；同一条错误只提示一次（SSE 断线时不会刷屏）。
      if (e.message !== lastError) {
        lastError = e.message;
        toast('拉取账本失败：' + e.message, 'err');
      }
    } finally {
      S.loading = false;
      inflight = null;
    }
    if (pending) {
      pending = false;
      refresh();
    }
  })();
  return inflight;
}

export function connect() {
  return api.stream(S.pbcId, refresh);
}

// 便捷读：当前单元与它的 Agent。
export function currentUnit() {
  return S.units.find((u) => u.id === S.pbcId) || null;
}

export function currentAgent() {
  const u = currentUnit();
  if (!u || !u.agentId) return null;
  return S.agents.find((a) => a.id === u.agentId) || null;
}

export function unitKind() {
  const u = currentUnit();
  return (u && u.kind) || 'pbc';
}

export function unitById(id) {
  return S.units.find((u) => u.id === id) || null;
}

export function agentById(id) {
  return S.agents.find((a) => a.id === id) || null;
}
