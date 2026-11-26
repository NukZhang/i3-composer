<script setup>
// Agent 工厂（v3.1）：把「生产 Agent 的人」和「被交付的 Agent」分开。
//
//   平台员工：写代码 / 跑测试 / 画原型 / 部署 / 续写 BRD 的执行者，固定 5 名，
//             不对外交付、没有评测集、不单独打 tag——升级它就是升级平台。
//   交付的 Agent：客户拿到手的那一屏工具，随交付单元走四阶段、有评测集、
//             能进发布批次——升级它就是一次交付。
import { computed } from 'vue';
import PageHeader from '../components/PageHeader.vue';
import { S } from '../store.js';
import { icon } from '../icons.js';

const products = computed(() => S.agents.filter((a) => a.product));
const workers = computed(() => S.agents.filter((a) => !a.product));
const STAGES = ['方案', '原型', '构建', '验证'];

function openAgent(id) {
  S.agentId = id;
  S.agTab = '0';
  S.view = 'agent';
}
function unitOf(a) {
  return S.units.find((u) => u.agentId === a.id) || null;
}
function delivered(a) {
  const u = unitOf(a);
  return !!(u && u.headTag) || !!a.published;
}
function busy(a) {
  return (a.st || []).includes('doing');
}
const ev = computed(() => products.value.reduce((n, x) => n + (x.eval ? x.eval[0] : 0), 0));
const en = computed(() => products.value.reduce((n, x) => n + (x.eval ? x.eval[1] : 0), 0));
const rate = computed(() => (en.value ? Math.round((ev.value / en.value) * 100) + '%' : '—'));
const deliveredCount = computed(() => products.value.filter(delivered).length);
</script>

<template>
  <PageHeader
    title="Agent 工厂"
    sub="员工是平台自带的执行者；被交付、被评测、被版本化的是他们生产出来的 Agent。两者不混在一个花名册里。"
  />

  <div class="row wrap" style="gap: 7px; margin-bottom: 11px">
    <div
      v-for="x in [
        ['平台员工', workers.length + ' 名', 'var(--tx-7)'],
        ['交付的 Agent', products.length + ' 个', 'var(--purple)'],
        ['在产', products.filter((a) => !delivered(a)).length + ' 个', 'var(--warn)'],
        ['已交付', deliveredCount + ' 个', 'var(--ok)'],
        ['评测通过率', rate, 'var(--info)'],
      ]"
      :key="x[0]"
      class="card tight"
      style="flex: 1; min-width: 130px; margin: 0; background: var(--bg-panel)"
    >
      <div class="small muted">{{ x[0] }}</div>
      <div :style="'font-size:17px;font-weight:650;color:' + x[2]">{{ x[1] }}</div>
    </div>
  </div>

  <!-- 交付的 Agent：产物，可点开看四阶段与版本 -->
  <div class="card">
    <div class="ch">
      <span v-html="icon('robot')" /><h3>交付的 Agent</h3>
      <span class="spacer" />
      <span class="small muted">点开看四阶段 + 版本</span>
    </div>
    <div v-if="!products.length" class="small muted">这个演示项目里还没有交付的 Agent。</div>
    <div v-for="a in products" :key="a.id" class="agent-row" @click="openAgent(a.id)">
      <div style="min-width: 0; flex: 1">
        <div class="row" style="gap: 7px">
          <span style="font-size: 13px; font-weight: 600; color: var(--tx-1)">{{ a.n }}</span>
          <span class="tag t-purple" style="font-size: 9px">产物</span>
          <span class="mono small" style="color: var(--info)">v{{ a.ver }}</span>
          <span v-if="delivered(a)" class="tag t-ok" style="font-size: 9px">已交付</span>
        </div>
        <div class="small muted" style="margin-top: 3px">
          所属单元 {{ unitOf(a) ? unitOf(a).name : '—' }} · {{ (a.tools || []).length }} 个工具 · {{ a.model }}
        </div>
      </div>
      <div class="hide-sm" style="display: inline-flex; gap: 8px; align-items: center">
        <span
          v-for="(s, i) in (a.st || []).slice(0, 4)"
          :key="i"
          style="display: inline-flex; align-items: center; gap: 3px; font-size: 9.5px"
          :style="{ color: s === 'todo' ? 'var(--tx-12)' : 'var(--tx-6)' }"
        >
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

  <!-- 平台员工：生产者，只读 -->
  <div class="card">
    <div class="ch">
      <span v-html="icon('users')" /><h3>平台员工</h3>
      <span class="spacer" />
      <span class="small muted">生产者 · 不单独打 tag</span>
    </div>
    <div v-for="a in workers" :key="a.id" class="row" style="padding: 8px 0; border-bottom: 1px solid var(--line-e); gap: 9px">
      <span style="font-size: 12.5px; font-weight: 600; color: var(--tx-2); min-width: 120px">{{ a.n }}</span>
      <span class="tag t-gray" style="font-size: 9px">{{ (a.role || '').replace('_', ' ') }}</span>
      <span class="small muted" style="flex: 1">{{ a.spe }}</span>
      <span class="tag" :class="busy(a) ? 't-warn' : 't-gray'" style="font-size: 9px">{{ busy(a) ? '在忙' : '空闲' }}</span>
      <span class="small muted" style="min-width: 150px; text-align: right">{{ a.task }}</span>
    </div>
  </div>

  <div class="card">
    <div class="ch"><span v-html="icon('cpu')" /><h3>为什么员工和产物要分开</h3></div>
    <div class="small muted" style="line-height: 1.8">
      员工是平台自带的执行者——写代码、跑测试、画原型、部署、续写 BRD。升级测试工程师等于升级平台，
      没有评测集，也不打 tag。<br />
      交付的 Agent 是客户拿到手的东西：它随单元走「方案 → 原型 → 构建 → 验证」，有版本号与评测集；
      评测不达标就不许打 tag —— 和 PBC 过四闸是同一件事。
    </div>
  </div>
</template>
