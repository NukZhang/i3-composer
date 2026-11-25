// 纯派生：只从 /api/state 的投影算，不产生新真相。
// 这些函数和 internal/domain 里的同名判断是一一对应的——
// 前端算它们只是为了显示（哪个制品过期、下一个版本号是多少），
// 判定本身（能不能打 tag）永远以服务端 gates 为准。

export const ART_ORDER = ['prd', 'proto', 'ad', 'at', 'backlog', 'code'];
export const ART = {
  prd: { n: 'PRD', icon: 'doc' },
  proto: { n: '原型', icon: 'layout' },
  ad: { n: 'AD', icon: 'layers' },
  at: { n: 'AT', icon: 'check' },
  backlog: { n: 'Backlog', icon: 'list' },
  code: { n: '代码', icon: 'cpu' },
};
export const PLAN_ARTS = ['prd', 'ad', 'at'];

export function latest(versions, t) {
  const a = (versions && versions[t]) || [];
  return a.length ? a[a.length - 1] : null;
}

export function findVer(versions, t, v) {
  return ((versions && versions[t]) || []).find((x) => x.v === v) || null;
}

export function drafts(versions) {
  const out = [];
  for (const t of ART_ORDER) {
    const l = latest(versions, t);
    if (l && l.st === 'draft') out.push({ t, v: l });
  }
  return out;
}

export function staleList(versions) {
  const out = [];
  for (const t of ART_ORDER) {
    const l = latest(versions, t);
    if (!l) continue;
    const bad = (l.df || []).filter((d) => {
      const up = latest(versions, d.t);
      return up && up.v !== d.v;
    });
    if (bad.length) out.push({ t, v: l, on: bad });
  }
  return out;
}

// 下一个制品版本号：1.0.2 → 1.0.3。和 domain.NextVersion 同规则。
export function nextVersion(cur) {
  if (!cur) return '1.0.1';
  const p = String(cur).split('.');
  if (p.length !== 3) return '1.0.1';
  return p[0] + '.' + p[1] + '.' + (Number(p[2]) + 1);
}

export function nextCommitNo(commits) {
  if (!commits || !commits.length) return '1.0.1';
  return nextVersion(commits[0].no);
}

export function nextTag(commits) {
  const tags = (commits || []).filter((c) => c.tag).map((c) => c.tag);
  if (!tags.length) return 'v1.0.0';
  const n = Math.max(...tags.map((t) => Number(String(t).replace(/^v/, '').split('.')[2]) || 0));
  return 'v1.0.' + (n + 1);
}

export function kindText(k) {
  return k === 'agent' ? 'Agent' : k === 'hybrid' ? 'PBC + Agent' : 'PBC';
}
export function kindCls(k) {
  return k === 'agent' ? 't-purple' : k === 'hybrid' ? 't-accent' : 't-info';
}

// 七步链路的执行者名：与 wf 的 workerFor 一致。
export const WORKER_NAME = {
  'x-fe': 'X-全栈工程师',
  'x-qa': 'X-测试工程师',
  'x-ui': 'X-原型工程师',
  'x-ops': 'X-运维工程师',
  'x-ba': 'X-业务分析工程师',
  // v3.1 起，质检与催缴是「交付的产物」，不是平台员工。
  // 老账本里的 id 仍可能出现在历史事实里，显示名跟着改，避免两种口径。
  'qa-agent': '质检 Agent',
  'dun-agent': '催缴话术 Agent',
  'x-qc': '质检 Agent',
  'x-dun': '催缴话术 Agent',
};

// ---------- Agent 编排（v2.6） ----------

// 七种节点的展示信息，与原型 AG_T 一致。
export const NODE_TYPE = {
  start: { n: '开始', col: 'var(--tx-9)', ic: 'play' },
  tool: { n: '工具', col: 'var(--info)', ic: 'box' },
  kb: { n: '知识库', col: 'var(--ok)', ic: 'doc' },
  llm: { n: '模型', col: 'var(--purple)', ic: 'cpu' },
  branch: { n: '分支', col: 'var(--warn)', ic: 'git' },
  hitl: { n: '人工', col: 'var(--err)', ic: 'users' },
  end: { n: '结束', col: 'var(--tx-7)', ic: 'check' },
};

export function nodeType(t) {
  return NODE_TYPE[t] || NODE_TYPE.tool;
}

// 画布坐标：与原型 agX / agY 同规则。
export function agX(n) {
  return 12 + n.c * 112;
}
export function agY(n) {
  return 14 + n.r * 98;
}

// 节点耗时：原型里 ms>1000 显示秒。
export function msText(ms) {
  if (!ms) return '—';
  return ms >= 1000 ? (ms / 1000).toFixed(1) + 's' : ms + 'ms';
}
export function tokText(tok) {
  if (!tok) return '—';
  return tok >= 1000 ? (tok / 1000).toFixed(1) + 'k' : String(tok);
}

// toolWiring 从节点配置里反查每个白名单工具用在哪。
// 白名单给了但流程里没接线，是一个有用的信号：要么流程漏了一步，要么权限该收回去。
export function toolWiring(agent) {
  const tools = (agent && agent.tools) || [];
  const nodes = (agent && agent.flow && agent.flow.nodes) || [];
  return tools.map((t) => {
    const used = nodes
      .filter((n) => {
        const c = n.cfg || {};
        if (c.tool === t) return true;
        if (Array.isArray(c.tools) && c.tools.indexOf(t) >= 0) return true;
        if (typeof c.pre === 'string' && c.pre.indexOf(t) >= 0) return true;
        return false;
      })
      .map((n) => n.n);
    return { tool: t, nodes: used, wired: used.length > 0 };
  });
}

// 编排整体统计：一轮多久、花多少 token。
export function flowStats(flow) {
  const nodes = (flow && flow.nodes) || [];
  return {
    nodes: nodes.length,
    edges: (flow && flow.edges ? flow.edges.length : 0),
    ms: nodes.reduce((a, n) => a + (n.ms || 0), 0),
    tok: nodes.reduce((a, n) => a + (n.tok || 0), 0),
    llm: nodes.filter((n) => n.t === 'llm').length,
  };
}

// 知识库问题单里待修的条数。
export function kbIssueTotal(kb) {
  if (!kb || !kb.issues) return 0;
  return kb.issues.reduce((a, x) => a + (x.c || 0), 0);
}

export function scoreColor(s) {
  return s >= 0.9 ? 'var(--ok)' : s >= 0.7 ? 'var(--warn)' : 'var(--err)';
}
export function scoreBg(s) {
  return s >= 0.9 ? 'var(--bg-ok-soft)' : s >= 0.7 ? 'var(--bg-warn-soft2)' : 'var(--bg-err-soft)';
}

export function evPct(x) {
  return (x * 100).toFixed(1) + '%';
}
export function evRate(run) {
  return run && run.total ? run.pass / run.total : 0;
}
export function evCur(a) {
  return a && a.runs && a.runs.length ? a.runs[0] : null;
}
export function evBase(a) {
  if (!a || !a.runs || !a.runs.length) return null;
  return a.runs.find((r) => r.base) || a.runs[1] || a.runs[0];
}
