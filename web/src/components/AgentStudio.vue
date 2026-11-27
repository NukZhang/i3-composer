<script setup>
// AgentStudio —— Agent 的构建页：编排工作台（原型 v2.6）。
//
// 左边是节点清单，中间是画布，下面左边是节点配置、右边是试跑。
// 写 Agent 的人干的活是「这条消息进来，先查哪个库、查不到怎么办、
// 什么时候调工具、什么时候找人确认」，所以这里给的是一张画布，
// 不是一份 YAML。
//
// 产物是「编排 + digest」：改节点、改提示词、改工具、换库版本都会换 digest；
// 没换 digest 就不是新版本，所以「改个提示词立刻生效」在这个产品里不存在。
import { computed, ref, watch } from 'vue';
import { S, toast, refresh, pushRuntime } from '../store.js';
import { api } from '../api.js';
import { icon } from '../icons.js';
import { agX, agY, nodeType, msText, tokText, toolWiring, toolsOutside, flowStats, evCur, evBase, evRate } from '../derive.js';

const props = defineProps({
  agent: { type: Object, default: null },
});

const flow = ref(null);
const sel = ref(null);
const dirty = ref(false);
const promptDraft = ref('');

// 每次重拉 Agent（试跑结果、保存、构建之后都会换一个 agent 对象）都会走到这里。
// 只有「换了一个 Agent」或「当前选中的节点在新编排里没了」才重设默认选中；
// 否则保留用户点的那一个。以前这里每次都重设成第一个节点，于是点完 kb 节点后
// 稍等片刻面板自己跳回开始节点——点选与「重设默认」抢着改同一个状态（#54）；
// e2e 在固定等待后读面板，读到哪个完全看异步刷新谁后到（#47 间歇失败的由来）。
const loadedId = ref(null);
watch(
  () => props.agent,
  (a) => {
    const id = a && a.id ? a.id : null;
    const sameAgent = id !== null && id === loadedId.value;
    flow.value = a && a.flow ? JSON.parse(JSON.stringify(a.flow)) : null;
    const ids = (flow.value && flow.value.nodes || []).map((n) => n.id);
    if (!sameAgent || !sel.value || !ids.includes(sel.value)) {
      sel.value = ids.length ? ids[0] : null;
    }
    loadedId.value = id;
    dirty.value = false;
    promptDraft.value = '';
  },
  { immediate: true },
);

const nodes = computed(() => (flow.value && flow.value.nodes) || []);
const edges = computed(() => (flow.value && flow.value.edges) || []);
const cur = computed(() => nodes.value.find((n) => n.id === sel.value) || null);
const stats = computed(() => flowStats(flow.value));
const wiring = computed(() => toolWiring(props.agent));
// 编排越界的工具在页面上也要点名：只在后端红一条闸，写 Agent 的人看不到是哪一步。
const outside = computed(() => toolsOutside(props.agent));
const kb = computed(() => (props.agent && props.agent.kb) || null);
const built = computed(() => (props.agent && props.agent.builtDigest) || '');
const dirtyBuild = computed(() => {
  if (!props.agent) return false;
  return props.agent.digest !== built.value;
});

const canvasW = computed(() => {
  const maxC = nodes.value.reduce((m, n) => Math.max(m, n.c), 0);
  return 12 + maxC * 112 + 106 + 14;
});
const canvasH = computed(() => {
  const maxR = nodes.value.reduce((m, n) => Math.max(m, n.r), 0);
  return 14 + maxR * 96 + 68 + 14;
});

// edgePath 画一条连线：同一列走竖线，不同列走横线，和原型一致。
function edgePath(e) {
  const a = nodes.value.find((n) => n.id === e[0]);
  const b = nodes.value.find((n) => n.id === e[1]);
  if (!a || !b) return '';
  let x1, y1, x2, y2, d;
  if (a.c === b.c) {
    x1 = agX(a) + 53; y1 = agY(a) + (a.r < b.r ? 60 : 0) - 2;
    x2 = agX(b) + 53; y2 = agY(b) + (a.r < b.r ? 0 : 60) + 2;
    d = `M ${x1} ${y1} C ${x1} ${y1 + 30}, ${x2} ${y2 - 30}, ${x2} ${y2}`;
  } else {
    x1 = agX(a) + 106; y1 = agY(a) + 27;
    x2 = agX(b); y2 = agY(b) + 27;
    d = `M ${x1} ${y1} C ${x1 + 46} ${y1}, ${x2 - 46} ${y2}, ${x2} ${y2}`;
  }
  return { d, mx: (x1 + x2) / 2, my: (y1 + y2) / 2 - 4 };
}

function select(id) {
  sel.value = id;
  const n = cur.value;
  promptDraft.value = n && n.cfg && n.cfg.prompt ? n.cfg.prompt : '';
}

// 改提示词：只改本地副本，保存后才进账本。
function editPrompt(v) {
  promptDraft.value = v;
  const n = cur.value;
  if (!n) return;
  n.cfg = n.cfg || {};
  n.cfg.prompt = v;
  dirty.value = true;
}

async function saveFlow() {
  if (!props.agent || !flow.value) return;
  try {
    const j = await api.agentFlow(props.agent.id, flow.value);
    pushRuntime('STATE', 'agent.flow.saved ' + j.digest);
    toast(j.dirty ? '编排已保存 · digest 变了，需要「构建」并重跑评测' : '编排已保存', 'ok');
  } catch (e) {
    toast(e.message, 'err');
  }
  await refresh();
}

async function build() {
  if (!props.agent) return;
  try {
    const j = await api.agentBuild(props.agent.id);
    if (j.changed) {
      pushRuntime('STATE', 'agent.built ' + j.ver + ' ' + j.digest);
      toast('已构建 ' + j.ver + ' · digest 更新，评测需重跑', 'ok');
    } else {
      toast('编排没变，不是新版本（没换 digest 就不是新版本）', 'warn');
    }
  } catch (e) {
    toast(e.message, 'err');
  }
  await refresh();
}

async function runTrial() {
  if (!props.agent) return;
  try {
    const j = await api.agentTrial(props.agent.id, {});
    pushRuntime('ACTOR', 'agent.trial.run ' + props.agent.id);
    S.agRan = true;
    toast('跑通一条' + (j.source === 'sample' ? '（回放定义里的样例）' : ''), 'ok');
  } catch (e) {
    toast(e.message, 'err');
  }
  await refresh();
}

async function toCase() {
  if (!props.agent) return;
  const t = props.agent.trialRun || props.agent.trial;
  if (!t) {
    toast('先跑一条，才有可沉淀的样例', 'warn');
    return;
  }
  try {
    await api.agentCase(props.agent.id, {
      v: '试跑沉淀',
      in: t.in,
      out: t.raw || (t.out || []).join(' / '),
      exp: '（待标注期望）',
      // 把当轮装配一起钉到用例上：以后这条失败了，点开还能看它当时读到了什么。
      assemblies: t.assemblies || [],
    });
    pushRuntime('ACTOR', 'agent.case.added ' + props.agent.id);
    toast('已沉淀为评测用例 · 进验证阶段的评测集；评测集变了，digest 随之变化，要重新构建并重跑评测', 'ok');
  } catch (e) {
    toast(e.message, 'err');
  }
  await refresh();
}

async function dispatchIssue(idx) {
  if (!props.agent) return;
  try {
    await api.kbDispatch(props.agent.id, idx);
    toast('已派给知识库 owner', 'ok');
  } catch (e) {
    toast(e.message, 'err');
  }
  await refresh();
}

function toolKind(t) {
  if (/(read|get|query|list|check|compare|screenshot|outline|run)/.test(t)) return '只读';
  if (/(send|apply|deploy|publish|commit|rollback|exec)/.test(t)) return '有副作用';
  if (/(write|draft|build|split|pick)/.test(t)) return '写';
  return '内部';
}
function approvalText(t) {
  const k = toolKind(t);
  return k === '只读' ? '不需要' : k === '有副作用' ? '人工点确认' : '进工作区待确认';
}
function approvalCls(t) {
  const k = toolKind(t);
  return k === '只读' ? 't-ok' : k === '有副作用' ? 't-err' : 't-warn';
}

const curRun = computed(() => evCur(props.agent));
const baseRun = computed(() => evBase(props.agent));
const dPass = computed(() => (curRun.value && baseRun.value ? curRun.value.pass - baseRun.value.pass : 0));
const dRatePct = computed(() => (curRun.value && baseRun.value ? (evRate(curRun.value) - evRate(baseRun.value)) * 100 : 0));

// 最近一次试跑的结果。优先用真跑的那次（trialRun），没有才回退到定义里的样例。
const trial = computed(() => props.agent && (props.agent.trialRun || props.agent.trial) || null);
const trialIn = computed(() => (trial.value && trial.value.in) || '');
const trialRaw = computed(() => (trial.value && trial.value.raw) || '');
const trialSource = computed(() => (trial.value && trial.value.source) || '');
const trialNodes = computed(() => (trial.value && trial.value.nodes) || []);
// 被拒的节点单独拣出来：只说「跑通」会让人以为工具真的调过了。
const trialBlocked = computed(() =>
  trialNodes.value.filter((n) => n.blocked && n.blocked.length),
);
// 当轮装配：试跑结果里 kb 节点带 assembly。按节点 id 找，选中哪个 kb 节点看哪个。
const trialAssemblies = computed(() => (trial.value && trial.value.assemblies) || []);
const curAssembly = computed(() => {
  const n = cur.value;
  if (!n) return null;
  return trialAssemblies.value.find((a) => a.node === n.id) || null;
});
</script>

<template>
  <div v-if="!agent || !flow" class="card"><div class="empty">这个 Agent 还没有编排。</div></div>
  <template v-else>
    <div class="as-note">
      <span v-html="icon('robot')" />
      <div>
        这一页是给 <b>Agent 开发员工</b> 用的：流程图决定它在什么情况下查库、调工具、找人确认；
        提示词和参数在右边配；改完必须<b>重新构建 + 重跑评测</b>——改提示词不是热更新。
      </div>
    </div>

    <div class="as-top">
      <span class="tag t-warn" style="font-size: 9px">草稿</span>
      <span class="mono small" style="color: var(--tx-2)">v{{ agent.ver }}</span>
      <span class="small muted">{{ stats.nodes }} 节点 · {{ stats.ms }}ms · {{ tokText(stats.tok) }} tok / 轮</span>
      <span v-if="dirtyBuild" class="tag t-warn" style="font-size: 9px">编排改了未构建</span>
      <span class="spacer" style="flex: 1" />
      <button class="btn small" @click="runTrial"><span v-html="icon('play', 11)" /> 试跑这一条</button>
      <button class="btn small" :disabled="!dirty" @click="saveFlow"><span v-html="icon('check', 11)" /> 保存编排</button>
      <button class="btn small primary" @click="build"><span v-html="icon('cpu', 11)" /> 构建 v{{ agent.ver }}</button>
    </div>

    <div class="as-cols">
      <div class="as-list">
        <div class="grp">流程 · {{ stats.nodes }} 个节点</div>
        <div
          v-for="n in nodes"
          :key="n.id"
          class="as-li"
          :class="{ on: sel === n.id }"
          @click="select(n.id)"
        >
          <span class="dot" :style="'background:' + nodeType(n.t).col" />
          <span style="white-space: nowrap; overflow: hidden; text-overflow: ellipsis">{{ n.n }}</span>
          <span class="k">{{ n.ms ? msText(n.ms) : '—' }}</span>
        </div>
        <div style="margin-top: 8px; padding: 0 4px">
          <button class="btn small" style="width: 100%" @click="toast('加节点：从标品里挑工具 / 知识库 / 模型（原型未做真连线）', 'ok')">
            <span v-html="icon('plus', 11)" /> 添加节点
          </button>
        </div>
      </div>

      <div class="as-canvas" :style="'min-width:' + canvasW + 'px;height:' + canvasH + 'px'">
        <svg :width="canvasW" :height="canvasH" style="position: absolute; left: 0; top: 0; pointer-events: none">
          <defs>
            <marker id="agAr" markerWidth="7" markerHeight="7" refX="6" refY="3" orient="auto">
              <path d="M0,0 L6,3 L0,6" fill="var(--line-i)" />
            </marker>
          </defs>
          <g v-for="(e, i) in edges" :key="i">
            <path
              v-if="edgePath(e)"
              :d="edgePath(e).d"
              fill="none"
              stroke="var(--line-i)"
              stroke-width="1.2"
              marker-end="url(#agAr)"
            />
            <text
              v-if="edgePath(e) && e[2]"
              :x="edgePath(e).mx"
              :y="edgePath(e).my"
              text-anchor="middle"
              font-size="8.5"
              fill="var(--tx-10)"
            >{{ e[2] }}</text>
          </g>
        </svg>
        <div
          v-for="n in nodes"
          :key="n.id"
          class="anode"
          :class="{ on: sel === n.id }"
          :style="'left:' + agX(n) + 'px;top:' + agY(n) + 'px;border-left:3px solid ' + nodeType(n.t).col"
          @click="select(n.id)"
        >
          <div class="h">
            <span :style="'width:5px;height:5px;border-radius:50%;background:' + nodeType(n.t).col + ';flex:0 0 5px'" />
            <span>{{ n.n }}</span>
            <span class="tp" :style="'color:' + nodeType(n.t).col + ';background:var(--bg-note)'">{{ nodeType(n.t).n }}</span>
          </div>
          <div class="s">{{ n.sub || '' }}</div>
          <div class="m">{{ n.ms ? msText(n.ms) + ' · ' + tokText(n.tok) + ' tok' : '—' }}</div>
        </div>
      </div>
    </div>

    <div class="as-bottom">
      <div class="as-cfg">
        <!-- data-node 标明这块面板此刻渲染的是哪个节点：切换选中时面板内容跟着换，
             e2e 与排查时能当场看出「读到的不是以为的那个节点」（#47）。 -->
        <div v-if="cur" :data-node="cur.id" data-testid="as-cfg-node" />
        <div class="row" style="gap: 6px; align-items: center; margin-bottom: 8px">
          <span style="font-size: 12px; font-weight: 600; color: var(--tx-1)">{{ cur ? cur.n : '—' }}</span>
          <span class="tag t-gray" style="font-size: 9px; margin-left: auto">{{ cur ? nodeType(cur.t).n : '' }}</span>
        </div>
        <div class="small muted" style="margin-bottom: 9px">节点 ID <span class="mono">{{ cur ? cur.id : '' }}</span></div>

        <template v-if="cur">
          <template v-if="cur.t === 'start'">
            <div class="as-lb">输入变量</div>
            <table class="tb tb-s">
              <thead><tr><th>变量</th><th style="width: 52px">类型</th><th style="width: 38px">必填</th><th>来源</th></tr></thead>
              <tbody>
                <tr v-for="(v, i) in (cur.cfg && cur.cfg.vars) || []" :key="i">
                  <td class="mono small">{{ v[0] }}</td><td class="small muted">{{ v[1] }}</td><td class="small">{{ v[2] }}</td><td class="small muted">{{ v[3] }}</td>
                </tr>
              </tbody>
            </table>
            <div class="as-tip">变量从哪来写清楚：调度器给的、还是上游制品带的。没人填的变量到运行时会空。</div>
          </template>

          <template v-else-if="cur.t === 'tool'">
            <div class="as-lb">工具</div>
            <div class="as-kv">
              <span class="mono" style="color: var(--info)">{{ cur.cfg && cur.cfg.tool }}</span>
              <span class="tag" :class="String((cur.cfg && cur.cfg.approve) || '').indexOf('不需要') === 0 ? 't-gray' : 't-warn'" style="font-size: 9px">{{ (cur.cfg && cur.cfg.approve) || '—' }}</span>
            </div>
            <div class="as-lb">入参映射</div>
            <div class="as-code">{{ (cur.cfg && cur.cfg.in) || '—' }}</div>
            <div class="as-tip">白名单外的工具调用会被直接拒绝，不做「灵活处理」。</div>
          </template>

          <template v-else-if="cur.t === 'kb'">
            <div class="as-lb">引用知识库</div>
            <div v-for="l in (kb && kb.libs) || []" :key="l.n" class="as-kv">
              <span>{{ l.n }}</span><span class="mono small" style="color: var(--tx-10)">{{ l.v }}</span>
            </div>
            <div class="row wrap" style="gap: 4px; margin: 8px 0 4px">
              <span class="achip">topK <b>{{ kb ? kb.topK : 5 }}</b></span>
              <span class="achip">阈值 <b>{{ kb ? kb.thr : 0.72 }}</b></span>
              <span class="achip">重排 <b>{{ kb && kb.rerank ? '开' : '关' }}</b></span>
              <span class="achip">出处 <b>必带</b></span>
            </div>
            <div class="as-lb">输出变量</div>
            <div class="as-code">{{ (cur.cfg && cur.cfg.out) || '—' }}</div>
            <!-- 当轮装配：这一轮这个 kb 节点到底载入了哪几篇、省了几篇、为什么。
                 只显示「跑通」回答不了「是不是没召回」，所以载入清单要能就地展开。 -->
            <template v-if="curAssembly">
              <div class="as-lb" style="margin-top: 8px">
                当轮装配
                <span class="small muted" style="font-weight: 400">
                  · 载入 {{ curAssembly.loaded.length }} 篇 · 省略 {{ curAssembly.omitted.count }} 篇
                </span>
              </div>
              <div class="small" style="color: var(--tx-5); line-height: 1.7">
                查询「{{ curAssembly.query || '（空）' }}」
                <span v-if="curAssembly.omitted.count">
                  ；省略原因：{{ curAssembly.omitted.reason }}
                </span>
                <span v-else>；没有省略（命中全部进了上下文）</span>
              </div>
              <div v-if="curAssembly.loaded.length" class="tree-box" style="margin-top: 6px">
                <div v-for="(c, i) in curAssembly.loaded" :key="i" class="tree-line">
                  <span class="k" style="width: 118px">{{ c.lib }}/{{ c.doc }}</span>
                  <span class="v">
                    <span class="tag t-gray" style="font-size: 8px">{{ c.class }}</span>
                    <span style="color: var(--tx-10)"> ver {{ c.ver || '未登记' }} · 命中 {{ c.score }}</span>
                  </span>
                </div>
              </div>
              <!-- 一篇没载入时按服务端算出的 note 归因，别写死「没召回」：
                   query 为空是「没读到输入」，读了没命中才是「没召回」，两种修法不同。 -->
              <div v-else class="small" style="color: var(--warn); margin-top: 4px">
                {{ curAssembly.note || '这一轮一篇都没载入' }}
              </div>
              <div class="small muted" style="margin-top: 4px">
                预算占用 {{ (curAssembly.budget.used * 100).toFixed(1) }}%（{{ Math.round(curAssembly.budget.used * 4000) }}/4000 字符）
              </div>
            </template>
            <div class="as-tip">库的版本写进 digest。换库不换版本，评测还停在旧库上，线上已经用新库回答。</div>
          </template>

          <template v-else-if="cur.t === 'llm'">
            <div class="as-lb">模型</div>
            <div class="as-kv">
              <span class="mono">{{ agent.model }}</span>
              <span class="small muted">temp {{ (cur.cfg && cur.cfg.temp) ?? '—' }}</span>
            </div>
            <div class="as-lb">提示词</div>
            <textarea
              v-if="cur.cfg && cur.cfg.prompt !== undefined"
              :value="promptDraft"
              spellcheck="false"
              style="width: 100%; min-height: 118px; font-family: ui-monospace, Menlo, monospace; font-size: 10px; line-height: 1.65; color: var(--tx-5); background: var(--bg-code); border: 1px solid var(--line-d); border-radius: 6px; padding: 7px 9px"
              @input="editPrompt($event.target.value)"
            />
            <div v-else class="as-code">（这个节点用方案阶段定稿的系统提示词，没有单独覆写。）</div>
            <div class="as-lb">输出变量</div>
            <div class="as-code">{{ (cur.cfg && cur.cfg.out) || '—' }}</div>
            <div class="as-tip">改这段字就是新版本：digest 会变，评测要重跑，通过率回退不许发布。</div>
          </template>

          <template v-else-if="cur.t === 'branch'">
            <template v-if="cur.cfg && cur.cfg.pre">
              <div class="as-lb">进入分支前先跑</div>
              <div class="as-code">{{ cur.cfg.pre }}</div>
            </template>
            <div class="as-lb">分支条件</div>
            <div v-for="(x, i) in (cur.cfg && cur.cfg.conds) || []" :key="i" class="as-kv" style="align-items: flex-start">
              <span class="mono small" style="color: var(--info)">{{ x[0] }}</span>
              <span class="small" style="color: var(--tx-3)">→ {{ x[1] }}</span>
            </div>
            <div class="as-tip">分支只看上游显式输出的字段，不让模型「自己判断要不要走这条」。</div>
          </template>

          <template v-else-if="cur.t === 'hitl'">
            <div class="as-lb">等谁</div>
            <div class="as-kv"><span>{{ (cur.cfg && cur.cfg.who) || '—' }}</span></div>
            <div class="as-lb">超时</div>
            <div class="as-kv"><span class="mono">{{ (cur.cfg && cur.cfg.timeout) || '—' }}</span></div>
            <div class="as-lb">超时后</div>
            <div class="as-code">{{ (cur.cfg && cur.cfg.onTimeout) || '—' }}</div>
            <div class="as-tip">人工节点是流程的一部分，不是异常。等人三天，流程状态要能存住。</div>
          </template>

          <template v-else>
            <div class="as-lb">输出字段</div>
            <table class="tb tb-s">
              <thead><tr><th>字段</th><th>来源</th></tr></thead>
              <tbody>
                <tr v-for="(x, i) in (cur.cfg && cur.cfg.outs) || []" :key="i">
                  <td class="mono small">{{ x[0] }}</td><td class="small muted">{{ x[1] }}</td>
                </tr>
              </tbody>
            </table>
            <div class="as-tip">结束节点决定这一轮的产物落到哪：制品树、还是待发送列表。</div>
          </template>
        </template>
      </div>

      <div class="as-dbg">
        <div class="as-dbg-l">
          <div class="as-dbg-h">
            <span v-html="icon('git', 11)" /> 试跑输入
            <span class="spacer" style="flex: 1" />
            <span class="small muted">一条真实样例</span>
          </div>
          <div class="as-code" style="margin: 0">{{ trialIn || '（没有样例）' }}</div>
          <div class="row" style="margin-top: 7px; gap: 6px">
            <button class="btn small" @click="runTrial"><span v-html="icon('play', 11)" /> 运行</button>
            <button v-if="S.agRan" class="btn small" @click="S.agRan = false">清空</button>
          </div>
        </div>
        <div class="as-dbg-r">
          <div class="as-dbg-h">
            <span v-html="icon('check', 11)" /> 输出
            <span class="spacer" style="flex: 1" />
            <span class="tag" :class="!S.agRan ? 't-gray' : trialBlocked.length ? 't-err' : 't-ok'" style="font-size: 9px">{{ !S.agRan ? '未运行' : trialBlocked.length ? '跑完 · 被拒 ' + trialBlocked.length + ' 步' : '跑通' }}</span>
          </div>
          <div v-if="!S.agRan" class="as-empty">还没跑过。点「运行」，看这一条在流程里怎么走下去、每个节点花多久。</div>
          <template v-else>
            <div class="as-code" style="margin: 0; max-height: 96px; overflow: auto">
              {{ trialRaw || '—' }}
            </div>
            <div class="as-chain">
              <template v-for="(n, i) in trialNodes" :key="n.id">
                <span :class="{ 'is-blocked': n.blocked && n.blocked.length }">{{ n.n }} <b>{{ msText(n.ms) }}</b></span><i v-if="i < trialNodes.length - 1">→</i>
              </template>
            </div>
            <div v-if="trialBlocked.length" class="small" style="margin-top: 6px; color: var(--err)">
              工具白名单拒了 {{ trialBlocked.length }} 步（这些工具没有真的被调用，只是流程继续往下走了）：
              <template v-for="(n, i) in trialBlocked" :key="'b' + n.id">
                <span>{{ n.n }}：{{ (n.blocked || []).join('；') }}</span><i v-if="i < trialBlocked.length - 1"> · </i>
              </template>
            </div>
            <div v-if="trialSource" class="small muted" style="margin-top: 6px">
              来源：{{ trialSource === 'executor' ? '执行器真跑（输入变了结论就会变）' : trialSource === 'sample' ? '回放定义里的样例' : '外部提交' }}
            </div>
            <div class="row" style="gap: 6px; margin-top: 7px">
              <button class="btn small" @click="toCase"><span v-html="icon('plus', 11)" /> 沉淀为评测用例</button>
              <span class="small muted" style="flex: 1">跑过的这一条会自动进验证阶段的评测集</span>
            </div>
          </template>
        </div>
      </div>
    </div>

    <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 9px; margin-top: 10px">
      <div class="card" style="margin: 0">
        <div class="ch"><span v-html="icon('git')" /><h3>本版改了什么</h3><span class="spacer" />
          <span class="mono small" style="color: var(--info)">v{{ (baseRun && baseRun.ver) || '—' }} → v{{ (curRun && curRun.ver) || agent.ver }}</span>
        </div>
        <div class="tree-box" style="margin: 0">
          <div class="tree-line"><span class="k">评测</span><span class="v">{{ (baseRun && baseRun.pass) || 0 }}/{{ (baseRun && baseRun.total) || 0 }} → <b :style="{ color: dPass >= 0 ? 'var(--ok)' : 'var(--err)' }">{{ agent.eval[0] }}/{{ agent.eval[1] }}</b>（{{ dPass >= 0 ? '+' : '' }}{{ dPass }} · {{ dRatePct >= 0 ? '+' : '' }}{{ dRatePct.toFixed(1) }}pp）</span></div>
          <div class="tree-line"><span class="k">成本</span><span class="v">{{ (baseRun && baseRun.cost) || '—' }} → {{ (curRun && curRun.cost) || '—' }}</span></div>
          <div class="tree-line"><span class="k">P95</span><span class="v">{{ (baseRun && baseRun.p95) || '—' }} → {{ (curRun && curRun.p95) || '—' }}</span></div>
          <div class="tree-line"><span class="k">digest</span><span class="v">{{ agent.digest }}</span></div>
        </div>
        <div class="small muted" style="margin-top: 7px">改节点、改提示词、改工具、改超时都换 digest。没换 digest 就不是新版本。</div>
      </div>

      <div class="card" style="margin: 0">
        <div class="ch"><span v-html="icon('box')" /><h3>这一份 Agent 由什么组成</h3><span class="spacer" />
          <span class="small muted">流程图 + 提示词 + 工具 + 知识库</span>
        </div>
        <div class="tree-box" style="margin: 0">
          <div class="tree-line"><span class="k">flow</span><span class="v">{{ stats.nodes }} 节点 · {{ stats.edges }} 条边</span></div>
          <div class="tree-line"><span class="k">prompt</span><span class="v">{{ (agent.prompt || '').length }} 字 + 各节点覆写</span></div>
          <div class="tree-line"><span class="k">tools</span><span class="v">{{ (agent.tools || []).length }} 个 · 白名单，越权即拒</span></div>
          <div class="tree-line"><span class="k">kb</span><span class="v">{{ kb ? kb.libs.length + ' 个库 · 锁到版本号' : '无（不依赖外部文档）' }}</span></div>
          <div class="tree-line"><span class="k">evalset</span><span class="v">{{ agent.eval[1] }} 条 · 版本锁定</span></div>
          <div class="tree-line"><span class="k">model</span><span class="v">{{ agent.model }}</span></div>
        </div>
      </div>
    </div>

    <div class="card">
      <div class="ch"><span v-html="icon('lock')" /><h3>工具权限</h3><span class="spacer" />
        <span class="small muted">白名单之外一律拒绝，不做「灵活处理」</span>
      </div>
      <div v-if="outside.length" class="small" style="margin: 0 0 8px; padding: 7px 9px; border-radius: 7px; background: var(--bg-err-soft3); border: 1px solid var(--line-err3); color: var(--err); line-height: 1.7">
        编排用了名单外的工具：<template v-for="(o, i) in outside" :key="o.tool"><b>{{ o.tool }}</b> 在 {{ o.nodes.join(' / ') }}<i v-if="i < outside.length - 1">；</i></template>
        ——试跑会拒这些节点，这条闸也红。
      </div>
      <table class="tb">
        <thead><tr><th style="width: 150px">工具</th><th style="width: 74px">类型</th><th style="width: 110px">审批</th><th>用在哪</th></tr></thead>
        <tbody>
          <tr v-for="u in wiring" :key="u.tool">
            <td class="mono small" style="color: var(--info)">{{ u.tool }}</td>
            <td class="small muted">{{ toolKind(u.tool) }}</td>
            <td><span class="tag" :class="approvalCls(u.tool)" style="font-size: 9px">{{ approvalText(u.tool) }}</span></td>
            <td class="small muted">
              <span v-if="u.wired">接到 {{ u.nodes.join(' / ') }}</span>
              <span v-else style="color: var(--warn)">白名单给了，流程里没接线</span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div v-if="kb" class="card">
      <div class="ch"><span v-html="icon('doc')" /><h3>知识库依赖</h3><span class="spacer" />
        <span class="small muted">Agent 的外部记忆，不是它自己学会的</span>
      </div>
      <table class="tb">
        <thead><tr><th>知识库</th><th style="width: 60px">文档</th><th style="width: 88px">版本</th><th style="width: 56px">同步</th><th style="width: 56px">归属</th></tr></thead>
        <tbody>
          <tr v-for="l in kb.libs" :key="l.n">
            <td class="small"><b style="font-weight: 550">{{ l.n }}</b></td>
            <td class="mono small muted">{{ l.docs }}</td>
            <td class="mono small">{{ l.v }}</td>
            <td class="small"><span class="tag" :class="l.sync === 'T+1' ? 't-ok' : 't-warn'" style="font-size: 9px">{{ l.sync }}</span></td>
            <td class="small muted">{{ l.owner }}</td>
          </tr>
        </tbody>
      </table>
      <div class="row wrap" style="gap: 6px; margin-top: 9px">
        <span class="achip">topK <b>{{ kb.topK }}</b></span>
        <span class="achip">相似度阈值 <b>{{ kb.thr }}</b></span>
        <span class="achip">重排 <b>{{ kb.rerank ? '开' : '关' }}</b></span>
        <span class="achip">引用 <b>必带出处</b></span>
      </div>
      <div class="small muted" style="margin-top: 9px; line-height: 1.75">
        知识库版本要写进 Agent 定义：<b>库换了、digest 没换，是最容易漏的一种发布事故</b>——
        评测还停在旧库上，线上已经用新库回答。所以换库必须重跑评测，和改提示词同等对待。
      </div>
    </div>

    <details class="as-exp">
      <summary>
        <span v-html="icon('cpu', 12)" /> 导出 Dapr Agent 定义
        <span class="small muted" style="margin-left: 8px">编排的序列化结果，人不直接改这个</span>
      </summary>
      <div class="tree-box" style="margin-top: 8px; font-size: 11px">
        <div class="tree-line"><span class="k">apiVersion</span><span class="v">dapr.io/v1alpha1</span></div>
        <div class="tree-line"><span class="k">kind</span><span class="v">Agent</span></div>
        <div class="tree-line"><span class="k">actor</span><span class="v">agent-{{ agent.id }}　（一个 Agent 一个 actor，同时只跑一轮）</span></div>
        <div class="tree-line"><span class="k">model</span><span class="v">{{ agent.model }}</span></div>
        <div class="tree-line"><span class="k">harness</span><span class="v">dsh · session 续跑，超时 900s</span></div>
        <div class="tree-line"><span class="k">flow</span><span class="v">{{ stats.nodes }} nodes / {{ stats.edges }} edges · 由编排生成</span></div>
        <div class="tree-line"><span class="k">tools</span><span class="v">{{ (agent.tools || []).join(' · ') }}</span></div>
        <div class="tree-line"><span class="k">workflow</span><span class="v">agent-lifecycle（方案→原型→构建→验证→发布）</span></div>
        <div class="tree-line"><span class="k">state</span><span class="v">statestore · key=session/{actorId}</span></div>
        <div class="tree-line"><span class="k">digest</span><span class="v">{{ agent.digest }}</span></div>
      </div>
      <div class="small muted" style="margin-top: 7px">
        改了编排，这份 YAML 会跟着变；但没人会拿 YAML 去改流程——那样改完看不见连线，也过不了评审。
      </div>
    </details>
  </template>
</template>
