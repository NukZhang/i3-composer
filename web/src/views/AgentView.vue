<script setup>
import { computed } from 'vue';
import { S, toast, refresh } from '../store.js';
import { api } from '../api.js';
import { icon } from '../icons.js';
import { evCur, evBase, evPct, evRate } from '../derive.js';

const STAGES = ['方案', '原型', '构建', '验证', '发布'];
const TABS = ['方案 · Agent 定义', '原型 · 用真实任务试跑', '构建 · Dapr Agent 定义', '验证 · 评测集', '发布 · 版本线'];

const a = computed(() => S.agents.find((x) => x.id === S.agentId) || S.agents[0] || null);
const tab = computed(() => String(S.agTab || '0'));
const cur = computed(() => evCur(a.value));
const base = computed(() => evBase(a.value));

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
      <button class="btn small primary" @click="publish"><span v-html="icon('cloud', 11)" /> 发布新版本</button>
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
        <div class="ch"><span v-html="icon('cpu')" /><h3>构建 · Dapr Agent 定义</h3><span class="spacer" /><span class="mono small">agents/{{ a.id }}.yaml</span></div>
        <div class="tree-box" style="font-size: 11px">
          <div class="tree-line"><span class="k">apiVersion</span><span class="v">dapr.io/v1alpha1</span></div>
          <div class="tree-line"><span class="k">kind</span><span class="v">Agent</span></div>
          <div class="tree-line"><span class="k">actor</span><span class="v">agent-{{ a.id }}　（一个 Agent 一个 actor，同一时刻只跑一轮）</span></div>
          <div class="tree-line"><span class="k">model</span><span class="v">{{ a.model }}</span></div>
          <div class="tree-line"><span class="k">harness</span><span class="v">dsh · session 续跑，超时 900s</span></div>
          <div class="tree-line"><span class="k">tools</span><span class="v">{{ (a.tools || []).join(' · ') }}</span></div>
          <div class="tree-line"><span class="k">workflow</span><span class="v">agent-lifecycle（方案→原型→构建→验证→发布）</span></div>
          <div class="tree-line"><span class="k">state</span><span class="v">statestore · key=session/{actorId}</span></div>
          <div class="tree-line"><span class="k">digest</span><span class="v">{{ a.digest }}</span></div>
        </div>
        <div class="small muted" style="margin-top: 8px">构建产物不是一个进程，是一份声明 + 一个 actor 类型。改工具或改提示词都会换 digest。</div>
      </div>
    </template>

    <template v-else-if="tab === '3'">
      <div class="ev-metrics">
        <div class="ev-m"><div class="ev-k">通过率</div><div class="ev-v">{{ cur ? evPct(evRate(cur)) : '—' }}</div><div class="ev-s">本轮</div></div>
        <div class="ev-m"><div class="ev-k">用例</div><div class="ev-v">{{ a.eval[0] }}/{{ a.eval[1] }}</div><div class="ev-s">通过 / 总数</div></div>
        <div class="ev-m"><div class="ev-k">总成本</div><div class="ev-v" style="color: var(--warn)">{{ (cur && cur.cost) || '—' }}</div><div class="ev-s">本轮累计</div></div>
        <div class="ev-m"><div class="ev-k">P95</div><div class="ev-v">{{ (cur && cur.p95) || '—' }}</div><div class="ev-s">P50 {{ (cur && cur.p50) || '—' }}</div></div>
        <div class="ev-m"><div class="ev-k">基线</div><div class="ev-v">v{{ (base && base.ver) || '—' }}</div><div class="ev-s">对比对象</div></div>
      </div>
      <div class="card">
        <div class="ch"><span v-html="icon('check')" /><h3>评测集</h3><span class="spacer" /><span class="tag" :class="a.eval[0] === a.eval[1] ? 't-ok' : 't-warn'">{{ a.eval[0] }}/{{ a.eval[1] }} 通过</span></div>
        <table class="tb">
          <thead><tr><th>用例</th><th style="width: 150px">期望</th><th style="width: 46px">结果</th><th style="width: 58px">耗时</th><th style="width: 54px">token</th></tr></thead>
          <tbody>
            <tr v-for="(c, i) in a.cases" :key="i">
              <td class="small">{{ c.v }}</td>
              <td class="small muted">{{ c.exp }}</td>
              <td><span class="ev-score" :style="'color:' + (c.score >= 0.9 ? 'var(--ok)' : c.score >= 0.7 ? 'var(--warn)' : 'var(--err)')">{{ c.score.toFixed(2) }}</span></td>
              <td class="mono small muted">{{ c.ms }}</td><td class="mono small muted">{{ c.tok }}</td>
            </tr>
          </tbody>
        </table>
        <div class="small muted" style="margin-top: 8px">评测集是带期望输出的固定用例；不过的用例挂在回执里，改完要重跑；通过率一旦比上一版低，就不许发布。</div>
      </div>
    </template>

    <template v-else>
      <div class="card">
        <div class="ch"><span v-html="icon('tag')" /><h3>发布 · 版本线</h3></div>
        <div class="tree-box">
          <div class="tree-line"><span class="k">v{{ a.ver }}</span><span class="v">当前 · 评测 {{ a.eval[0] }}/{{ a.eval[1] }} · {{ a.published ? '已注册 agent/' + a.id + ' · 流量 ' + a.rollout + '%' : '未发布' }}</span></div>
          <div class="tree-line"><span class="k">v{{ (base && base.ver) || '—' }}</span><span class="v">上一版 · 回滚目标</span></div>
          <div v-if="a.rollbackTo" class="tree-line"><span class="k">回滚</span><span class="v">已回滚到 v{{ a.rollbackTo }}</span></div>
        </div>
        <div class="small muted" style="margin-top: 9px">发布＝把 actor 类型指向新 digest，不是「改个提示词立即生效」。回滚同样是一条命令。</div>
      </div>
    </template>
  </template>
</template>
