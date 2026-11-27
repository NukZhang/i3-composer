<script setup>
import { computed } from 'vue';
import EvalWorkbench from '../components/EvalWorkbench.vue';
import KbAccuracy from '../components/KbAccuracy.vue';
import { S, toast, refresh } from '../store.js';
import { api } from '../api.js';
import { icon } from '../icons.js';
import { evCur, evBase, evPct, evRate, nodeType, flowStats, toolWiring } from '../derive.js';

const can = (a) => (S.acts || []).includes(a);
const STAGES = ['方案', '原型', '构建', '验证', '发布'];
const TABS = ['方案 · Agent 定义', '原型 · 用真实任务试跑', '构建 · 编排摘要', '验证 · 评测集', '发布 · 版本线'];

const a = computed(() => S.agents.find((x) => x.id === S.agentId) || S.agents[0] || null);
const tab = computed(() => String(S.agTab || '0'));
const cur = computed(() => evCur(a.value));
const base = computed(() => evBase(a.value));
const stats = computed(() => flowStats(a.value && a.value.flow));
const wiring = computed(() => toolWiring(a.value));

// 去编排页：这个 Agent 挂在哪个交付单元上，就去那个单元的「Agent 编排」tab。
function goStudio() {
  const u = (S.units || []).find((x) => x.agentId === (a.value && a.value.id));
  if (!u) {
    toast('这个 Agent 还没挂到交付单元上', 'warn');
    return;
  }
  S.pbcId = u.id;
  S.bpTab = 'agentdef';
  S.view = 'build';
}

async function publish() {
  try {
    const j = await api.agentPublish(a.value.id);
    toast('评测通过，已注册 v' + j.ver, 'ok');
  } catch (e) {
    toast(e.message, 'err');
  }
  await refresh();
}
function stageMark(s) {
  return s === 'done' ? '✓' : s === 'doing' ? '●' : '○';
}
function stageColor(s) {
  return s === 'done' ? 'var(--ok)' : s === 'doing' ? 'var(--warn)' : 'var(--tx-12)';
}
</script>

<template>
  <div v-if="!a" class="card"><div class="empty">还没有 Agent 定义。</div></div>
  <template v-else>
    <div class="crumb"><a @click="S.view = 'workers'">Agent 工厂</a>&nbsp;/&nbsp;{{ a.n }}</div>
    <div class="ph">
      <div>
        <h1>{{ a.n }} <span class="tag t-purple" style="vertical-align: 4px">{{ a.role }}</span></h1>
        <div class="sub">v{{ a.ver }} · Dapr actor <code style="color: var(--info)">agent/{{ a.id }}</code> · 模型 {{ a.model }}</div>
      </div>
      <div class="spacer" />
      <button class="btn small" @click="toast('试跑要接执行器（DSH），本服务只接收结果', 'warn')"><span v-html="icon('play', 11)" /> 试跑</button>
      <!-- 发布是 SA 的职责（服务端 ActTag 兜底）。前端先拦一道，是告诉别人「这不是你的活」，
           而不是把按钮递到人手里再吃 403（宪法 V：越权不画绿）。其余角色可见但禁用、带 title 说明原因。 -->
      <button class="btn small primary" :disabled="!can('tag')" :title="can('tag') ? '' : '发布新版本是 SA 的职责'" @click="publish"><span v-html="icon('cloud', 11)" /> 发布新版本</button>
    </div>

    <div class="ptabs">
      <div v-for="(n, i) in STAGES" :key="n" class="ptab" :class="{ on: tab === String(i) }" @click="S.agTab = String(i)">
        <span :style="{ color: stageColor(a.st[i]) }">{{ stageMark(a.st[i]) }}</span> {{ n }}
      </div>
      <div class="spacer" />
      <span class="small muted">当前 {{ a.task }}</span>
    </div>

    <template v-if="tab === '0'">
      <div class="card">
        <div class="ch"><span v-html="icon('doc')" /><h3>方案 · Agent 定义</h3><span class="spacer" /><span class="small muted">改这个是新版本，不是热更新</span></div>
        <div class="small muted" style="margin-bottom: 6px">系统提示词</div>
        <div class="pv" style="border: 1px solid var(--line-1); border-radius: 8px; padding: 11px 14px; font-size: 12px; line-height: 1.85">{{ a.prompt }}</div>
        <div class="small muted" style="margin: 11px 0 6px">工具白名单（超出即拒，不做「灵活处理」）</div>
        <div class="row wrap" style="gap: 5px"><span v-for="t in a.tools" :key="t" class="achip"><b>{{ t }}</b></span></div>
        <div class="small muted" style="margin-top: 9px">边界：不能提交版本、不能发布、不能改别人的制品。产出必须进对应人类角色的待确认。</div>
      </div>
    </template>

    <template v-else-if="tab === '1'">
      <div class="card">
        <div class="ch"><span v-html="icon('layout')" /><h3>原型 · 用真实任务试跑</h3><span class="spacer" /><span class="small muted">不写代码，只看它怎么接活</span></div>
        <div class="chatbox">
          <div class="msg user"><div class="who">调度器</div><div class="bub">领到 {{ (S.inbox.find((x) => x.pbc === S.pbcId) || {}).t || '一条真实任务' }}，请按 AT 执行。</div></div>
          <div class="msg bot"><div class="who">{{ a.n }}</div><div class="bub">{{ (a.cases && a.cases[0] && a.cases[0].out) || '（本轮没有产出）' }}</div></div>
        </div>
      </div>
    </template>

    <template v-else-if="tab === '2'">
      <div class="card">
        <div class="ch"><span v-html="icon('cpu')" /><h3>构建 · 编排摘要</h3><span class="spacer" />
          <span class="mono small" style="color: var(--info)">digest {{ a.digest }}</span>
          <button class="btn small" style="margin-left: 8px" @click="goStudio"><span v-html="icon('git', 11)" /> 去编排页</button>
        </div>
        <div class="tree-box" style="font-size: 11px">
          <div class="tree-line"><span class="k">flow</span><span class="v">{{ stats.nodes }} 节点 · {{ stats.edges }} 条边</span></div>
          <div class="tree-line"><span class="k">llm</span><span class="v">{{ stats.llm }} 个模型节点</span></div>
          <div class="tree-line"><span class="k">tools</span><span class="v">{{ (a.tools || []).join(' · ') }}</span></div>
          <div class="tree-line"><span class="k">actor</span><span class="v">agent-{{ a.id }}　（一个 Agent 一个 actor，同一时刻只跑一轮）</span></div>
          <div class="tree-line"><span class="k">harness</span><span class="v">dsh · session 续跑，超时 900s</span></div>
          <div class="tree-line"><span class="k">workflow</span><span class="v">agent-lifecycle（方案→原型→构建→验证→发布）</span></div>
        </div>
      </div>
      <div class="card">
        <div class="ch"><span v-html="icon('layers')" /><h3>节点一览</h3><span class="spacer" /><span class="small muted">编排的骨架；细节去编排页改</span></div>
        <div class="row wrap" style="gap: 5px">
          <span v-for="n in (a.flow && a.flow.nodes) || []" :key="n.id" class="achip">
            <b :style="{ color: nodeType(n.t).col }">{{ n.n }}</b>
            <span style="color: var(--tx-11); margin-left: 5px">{{ nodeType(n.t).n }}</span>
          </span>
        </div>
        <div class="small muted" style="margin-top: 9px">构建产物不是一个进程，是一份编排 + 一个 actor 类型。改节点、改提示词、改工具都会换 digest。</div>
      </div>
      <div class="card">
        <div class="ch"><span v-html="icon('lock')" /><h3>工具权限</h3><span class="spacer" /><span class="small muted">白名单之外一律拒绝</span></div>
        <table class="tb">
          <thead><tr><th style="width: 150px">工具</th><th>用在哪</th></tr></thead>
          <tbody>
            <tr v-for="u in wiring" :key="u.tool">
              <td class="mono small" style="color: var(--info)">{{ u.tool }}</td>
              <td class="small muted">
                <span v-if="u.wired">接到 {{ u.nodes.join(' / ') }}</span>
                <span v-else style="color: var(--warn)">白名单给了，流程里没接线</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>

    <template v-else-if="tab === '3'">
      <EvalWorkbench :agent="a" />
      <KbAccuracy :agent="a" :kb="a && a.kb" />
    </template>

    <template v-else>
      <div class="card">
        <div class="ch"><span v-html="icon('tag')" /><h3>发布 · 版本线</h3></div>
        <div class="tree-box">
          <div class="tree-line">
            <span class="k">v{{ a.ver }}</span>
            <span class="v">当前 · 评测 {{ a.eval[0] }}/{{ a.eval[1] }}</span>
          </div>
          <div class="tree-line">
            <span class="k">注册状态</span>
            <span class="v">
              <template v-if="a.published">已注册 agent/{{ a.id }} · 灰度档位 {{ a.rollout }}%（未接流量分流）</template>
              <template v-else>未发布</template>
            </span>
          </div>
          <div class="tree-line"><span class="k">v{{ (base && base.ver) || '—' }}</span><span class="v">上一版 · 回滚目标</span></div>
          <div v-if="a.rollbackTo" class="tree-line"><span class="k">回滚</span><span class="v">已回滚到 v{{ a.rollbackTo }}</span></div>
        </div>
        <div class="small muted" style="margin-top: 9px">发布会在账本中登记版本与摘要。</div>
      </div>
    </template>
  </template>
</template>
