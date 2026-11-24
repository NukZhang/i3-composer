<script setup>
import { computed } from 'vue';
import PageHeader from '../components/PageHeader.vue';
import { S, refresh, toast } from '../store.js';
import { api } from '../api.js';
import { icon } from '../icons.js';

const busy = computed(() => S.agents.filter((a) => (a.st || []).includes('doing')).length);
const published = computed(() => S.agents.filter((a) => a.published).length);
const ev = computed(() => S.agents.reduce((a, x) => a + (x.eval ? x.eval[0] : 0), 0));
const en = computed(() => S.agents.reduce((a, x) => a + (x.eval ? x.eval[1] : 0), 0));
const STAGES = ['方案', '原型', '构建', '验证', '发布'];

function openAgent(id) {
  S.agentId = id;
  S.agTab = '0';
  S.view = 'agent';
}
async function publish(a) {
  try {
    const j = await api.agentPublish(a.id);
    toast('评测通过，已注册 v' + j.ver, 'ok');
  } catch (e) {
    toast(e.message, 'err');
  }
  await refresh();
}
</script>

<template>
  <PageHeader
    title="Agent 工厂 · X"
    sub="Agent 也是被生产出来的制品：方案 → 原型 → 构建 → 验证 → 发布。构建基于 Dapr Agent 框架，执行走 DSH；不是写死的角色。"
  />

  <div class="row wrap" style="gap: 7px; margin-bottom: 11px">
    <div v-for="x in [['在产', busy + ' 个', 'var(--warn)'], ['已发布', published + ' 个', 'var(--ok)'], ['评测通过率', Math.round((ev / Math.max(1, en)) * 100) + '%', 'var(--info)'], ['共 ' + S.agents.length + ' 名', '全部 Worker 型', 'var(--tx-7)']]" :key="x[0]" class="card tight" style="flex: 1; min-width: 150px; margin: 0; background: var(--bg-panel)">
      <div class="small muted">{{ x[0] }}</div>
      <div :style="'font-size:17px;font-weight:650;color:' + x[2]">{{ x[1] }}</div>
    </div>
  </div>

  <div class="card">
    <div class="ch"><span v-html="icon('users')" /><h3>在产与已发布</h3><span class="spacer" /><span class="small muted">点开看五阶段</span></div>
    <div v-for="a in S.agents" :key="a.id" class="agent-row" @click="openAgent(a.id)">
      <div style="min-width: 0; flex: 1">
        <div class="row" style="gap: 7px">
          <span style="font-size: 13px; font-weight: 600; color: var(--tx-1)">{{ a.n }}</span>
          <span class="tag t-purple" style="font-size: 9px">{{ (a.role || '').replace('_', ' ') }}</span>
          <span class="mono small" style="color: var(--info)">v{{ a.ver }}</span>
          <span v-if="a.published" class="tag t-ok" style="font-size: 9px">已发布 · {{ a.rollout }}%</span>
        </div>
        <div class="small muted" style="margin-top: 3px">{{ a.spe }} · 当前 {{ a.task }}</div>
      </div>
      <div class="hide-sm" style="display: inline-flex; gap: 8px; align-items: center">
        <span v-for="(s, i) in a.st" :key="i" style="display: inline-flex; align-items: center; gap: 3px; font-size: 9.5px" :style="{ color: s === 'todo' ? 'var(--tx-12)' : 'var(--tx-6)' }">
          <span :style="'width:6px;height:6px;border-radius:50%;background:' + (s === 'done' ? 'var(--ok)' : s === 'doing' ? 'var(--warn)' : 'var(--line-i)')" />{{ STAGES[i] }}
        </span>
      </div>
      <div style="text-align: right; min-width: 88px">
        <div class="small" :style="{ color: a.eval[0] === a.eval[1] ? 'var(--ok)' : 'var(--warn)' }">{{ a.eval[0] }}/{{ a.eval[1] }} 评测</div>
        <div class="small muted">{{ a.cost }}</div>
      </div>
      <span class="small" style="color: var(--tx-10)" v-html="icon('chev', 13)" />
    </div>
  </div>

  <div class="card">
    <div class="ch"><span v-html="icon('cpu')" /><h3>为什么 Agent 要走五阶段</h3></div>
    <div class="small muted" style="line-height: 1.8">
      Agent 不是「配一个提示词就上线」的角色，它和 PBC 一样是交付物：
      <b>方案</b>定提示词与工具边界，<b>原型</b>用真实任务试跑，<b>构建</b>出 Dapr Agent 定义，<b>验证</b>跑评测集，<b>发布</b>注册到运行时并留版本号。
      <br />改一次提示词就是一次新版本，评测通过率回退就不许发布——和 PBC 打 tag 前要过四闸是同一件事。
    </div>
  </div>

  <div class="card">
    <div class="ch"><span v-html="icon('check')" /><h3>未发布 / 待发布</h3><span class="spacer" /><span class="small muted">四闸全过才能发布（SA）</span></div>
    <div v-for="a in S.agents.filter((x) => !x.published)" :key="a.id" class="row" style="padding: 8px 0; border-bottom: 1px solid var(--line-e)">
      <span class="mono small" style="color: var(--info)">v{{ a.ver }}</span>
      <span class="small">{{ a.n }}</span>
      <span class="small muted">{{ a.eval[0] }}/{{ a.eval[1] }} · 通过率 {{ ((a.eval[0] / Math.max(1, a.eval[1])) * 100).toFixed(1) }}%</span>
      <span class="spacer" style="flex: 1" />
      <button class="btn small primary" @click="publish(a)"><span v-html="icon('cloud', 11)" /> 发布</button>
    </div>
    <div v-if="!S.agents.filter((x) => !x.published).length" class="small muted">所有 Agent 都已发布。</div>
  </div>
</template>
