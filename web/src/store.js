// store.js —— 一个极小的响应式 store。
//
// 为什么不用 Pinia：这个应用只有一个全局状态（当前投影 + 视图状态），
// 引入一个状态库的收益抵不过「多一层间接」的成本。这里只用 Vue 的
// reactive + 一个 refresh 串行器，语义和 v2.1 的 adapter 完全一致。

import { reactive } from 'vue';
import { api } from './api.js';

export const S = reactive({
  // 视图
  view: 'home',        // home | projects | brd | plan | proto | build | verify | orch | agent | workers | item | notify | tech | config
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

  // v2.7：工作空间 → 项目。当前项目挂在这两个 id 上。
  wsId: '',
  projId: '',
  wsOpen: {},     // 工作空间展开状态（纯展示态，不进账本）
  addOpen: false, // 「＋ 新建项目 / 工作空间」弹窗
  addTab: 'new',  // new | join
  addKind: 'proj',// proj | ws

  // 投影（全部来自 /api/*，不持有第二份真相）
  state: null,      // /api/state
  units: [],        // /api/units
  projects: [],     // /api/projects → projects
  workspaces: [],   // /api/projects → workspaces
  catalog: { projects: [], spaces: [] },
  projUnits: {},    // 项目 id → 单元数
  brd: null,        // /api/brd?proj=
  agents: [],       // /api/agents
  facts: [],        // /api/facts
  notifications: [],// /api/notifications
  inbox: [],        // /api/inbox

  // 纯展示态（不进账本）
  toasts: [],
  runtime: [],      // 右栏运行时日志
  // 每次重拉投影自增：账本外的东西（如发布批次）没有状态可挂，
  // 订阅者看它来判断「该重新读一遍了」。
  tick: 0,
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
  // 需求池是跨单元同表：/api/inbox 一直在，空项目也照样能拉。
  const ib = await api.inbox();
  S.inbox = ib.items || [];
  S.role = ib.role;
  S.roleName = ib.name;
  S.acts = ib.acts || [];
  // 空项目没有交付单元：不拉 /api/state（那需要一个 pbc），状态置空。
  if (!S.pbcId) {
    S.state = null;
    S.selCommit = '';
    return;
  }
  const st = await api.state(S.pbcId);
  S.state = st;
  if (!S.selCommit || !(st.commits || []).some((c) => c.no === S.selCommit)) {
    S.selCommit = st.commits && st.commits.length ? st.commits[0].no : '';
  }
}

async function loadGlobal() {
  const [units, agents, pj, notifications] = await Promise.all([
    api.units(),
    api.agents(),
    api.projects(),
    api.notifications(),
  ]);
  S.units = units.units || [];
  S.agents = agents.agents || [];
  S.projects = pj.projects || [];
  S.workspaces = pj.workspaces || [];
  S.catalog = pj.catalog || { projects: [], spaces: [] };
  S.projUnits = pj.unitCount || {};
  S.notifications = notifications.notifications || [];

  // 当前项目：沿用已知的；没有或已经被删就落到第一个项目。
  const ids = S.projects.map((p) => p.id);
  if (!S.projId || ids.indexOf(S.projId) < 0) {
    S.projId = S.projects.length ? S.projects[0].id : '';
  }
  const cur = currentProject();
  S.wsId = (cur && cur.ws) || '';

  // 当前交付单元必须属于当前项目：切项目时单元跟着换。
  const mine = S.units.filter((u) => u.proj === S.projId);
  if (!S.pbcId || !mine.some((u) => u.id === S.pbcId)) {
    S.pbcId = mine.length ? mine[0].id : '';
  }

  // BRD 是项目级的：跟着当前项目走。
  S.brd = S.projId ? await api.brd(S.projId) : null;
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
      S.facts = S.pbcId ? (await api.facts(S.pbcId)).facts || [] : [];
      S.tick++;
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
  // SSE 是按账本推的；空项目也要能收到「项目/BRD 变了」的通知。
  return api.stream(S.pbcId || 'units', refresh);
}

// 便捷读：当前单元与它的 Agent。
export function currentUnit() {
  return S.units.find((u) => u.id === S.pbcId) || null;
}

// currentProject / currentWorkspace 是 v2.7 的上下文。
export function currentProject() {
  return S.projects.find((p) => p.id === S.projId) || null;
}
export function currentWorkspace() {
  return S.workspaces.find((w) => w.id === S.wsId) || null;
}
export function unitsOfProject(pid) {
  return S.units.filter((u) => u.proj === pid);
}
// projectTitle 是侧栏 / 面包屑用的「工作空间 / 项目」。
export function projectTitle() {
  const p = currentProject();
  if (!p) return '—';
  const w = currentWorkspace();
  return (w ? w.name + ' / ' : '') + p.name;
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
