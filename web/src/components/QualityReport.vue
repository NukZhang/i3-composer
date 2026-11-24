<script setup>
// QualityReport —— 构建页的「质量报告」：机器自检。
//
// 这是构建跑完后的自检（四闸、还原、评测台、知识库准确度），
// 判定归验证阶段的人：机器说「做出来没有、像不像」，
// 人要说「能不能用、好不好用」。
import { computed } from 'vue';
import EvalWorkbench from './EvalWorkbench.vue';
import KbAccuracy from './KbAccuracy.vue';
import { icon } from '../icons.js';

const props = defineProps({
  kind: { type: String, default: 'pbc' },
  gates: { type: Object, default: () => ({ all: [], pbc: [], agent: [] }) },
  agent: { type: Object, default: null },
  head: { type: String, default: '' },
  svc: { type: Object, default: null },
});

const all = computed(() => props.gates.all || []);
const pbcGates = computed(() => props.gates.pbc || []);
const agGates = computed(() => props.gates.agent || []);
const passAll = computed(() => all.value.filter((g) => g.ok).length);
const canRelease = computed(() => all.value.length > 0 && passAll.value === all.value.length);
</script>

<template>
  <div>
    <!-- 组合项：合并门禁 -->
    <div v-if="kind === 'hybrid'" class="card" :style="'border-color:' + (canRelease ? 'var(--line-ok)' : 'var(--line-err)')">
      <div class="ch">
        <span v-html="icon('check')" /><h3>合并门禁 · 一次 tag 锁两条产物</h3><span class="spacer" />
        <span class="tag" :class="canRelease ? 't-ok' : 't-err'">{{ canRelease ? '可以发布' : '不能发布' }}</span>
      </div>
      <div style="display: grid; grid-template-columns: 1fr 62px 1fr; gap: 0; align-items: center">
        <div class="ev-exp" style="min-width: 0">
          <div class="ev-tag">服务侧 · PBC</div>
          <div style="font-size: 12px; font-weight: 600; color: var(--tx-1); margin-bottom: 4px">{{ pbcGates.filter((g) => g.ok).length }}/4 通过</div>
          <div v-for="g in pbcGates" :key="g.key" class="row" style="gap: 6px; padding: 3px 0; font-size: 11px">
            <b :style="{ color: g.ok ? 'var(--ok)' : 'var(--err)' }">{{ g.ok ? '✓' : '✕' }}</b>
            <span style="color: var(--tx-3)">{{ g.label }}</span>
            <span class="spacer" style="flex: 1" /><span class="small muted">{{ g.detail }}</span>
          </div>
        </div>
        <div :style="'text-align:center;font-size:16px;color:' + (canRelease ? 'var(--ok)' : 'var(--err)')">{{ canRelease ? '→' : '✕' }}</div>
        <div class="ev-exp" style="min-width: 0">
          <div class="ev-tag">Agent 侧 · {{ agent && agent.n }}</div>
          <div style="font-size: 12px; font-weight: 600; color: var(--tx-1); margin-bottom: 4px">{{ agGates.filter((g) => g.ok).length }}/4 通过</div>
          <div v-for="g in agGates" :key="g.key" class="row" style="gap: 6px; padding: 3px 0; font-size: 11px">
            <b :style="{ color: g.ok ? 'var(--ok)' : 'var(--err)' }">{{ g.ok ? '✓' : '✕' }}</b>
            <span style="color: var(--tx-3)">{{ g.label }}</span>
            <span class="spacer" style="flex: 1" /><span class="small muted">{{ g.detail }}</span>
          </div>
        </div>
      </div>
      <div class="small muted" style="margin-top: 8px; line-height: 1.7">
        {{ canRelease
          ? '两侧都过，可以打 tag。tag 同时锁镜像 digest 与 Agent digest，回滚必须一起回。'
          : '不能发：' + (svc && svc.pass !== svc.total ? '服务侧还有 ' + (svc.total - svc.pass) + ' 条验收未过；' : '') + (agGates.find((g) => g.key === 'agent-eval' && !g.ok) ? 'Agent 评测未达 95%。' : '') + '组合项最怕的就是只验一半。' }}
      </div>
    </div>

    <!-- 单侧四闸 -->
    <div v-else class="card">
      <div class="ch">
        <span v-html="icon('check')" /><h3>交付四闸 · {{ head || '—' }}</h3><span class="spacer" />
        <span class="tag" :class="passAll === 4 ? 't-ok' : 't-warn'">{{ passAll }}/4 通过</span>
      </div>
      <div class="row wrap" style="gap: 6px">
        <span
          v-for="g in all"
          :key="g.key"
          class="achip"
          :style="'border-color:' + (g.ok ? 'var(--line-ok)' : 'var(--line-err)')"
        >
          <b :style="{ color: g.ok ? 'var(--ok)' : 'var(--err)' }">{{ g.ok ? '✓' : '✕' }}</b> {{ g.label }}
          <span style="color: var(--tx-9); margin-left: 5px">{{ g.detail }}</span>
        </span>
        <span v-if="!all.length" class="small muted">还没有提交，四闸无从判定。</span>
      </div>
    </div>

    <!-- 组合项：服务侧 / Agent 侧各自完整覆盖 -->
    <template v-if="kind === 'hybrid'">
      <div style="margin-top: 16px">
        <div class="row" style="gap: 7px; margin-bottom: 8px; align-items: baseline">
          <span style="font-size: 15px; font-weight: 650; color: var(--tx-1)">② 服务侧判据</span>
          <span class="small muted">四闸 + 验收清单 · 和其它 PBC 同一套标准</span>
        </div>
      </div>
      <div class="card">
        <div class="ch"><span v-html="icon('check')" /><h3>服务四闸</h3><span class="spacer" />
          <span class="tag" :class="pbcGates.filter((g) => g.ok).length === 4 ? 't-ok' : 't-err'">{{ pbcGates.filter((g) => g.ok).length }}/4</span>
        </div>
        <div class="row wrap" style="gap: 6px">
          <span
            v-for="g in pbcGates"
            :key="g.key"
            class="achip"
            :style="'border-color:' + (g.ok ? 'var(--line-ok)' : 'var(--line-err)')"
          >
            <b :style="{ color: g.ok ? 'var(--ok)' : 'var(--err)' }">{{ g.ok ? '✓' : '✕' }}</b> {{ g.label }}
            <span style="color: var(--tx-9); margin-left: 5px">{{ g.detail }}</span>
          </span>
        </div>
      </div>
      <div class="card">
        <div class="ch"><span v-html="icon('list')" /><h3>验收清单</h3><span class="spacer" />
          <span class="tag" :class="svc && svc.pass === svc.total ? 't-ok' : 't-err'">{{ svc ? svc.pass : 0 }}/{{ svc ? svc.total : 0 }}</span>
        </div>
        <template v-if="svc && svc.fails && svc.fails.length">
          <div v-for="(f, i) in svc.fails" :key="i" class="row" style="padding: 7px 0; border-bottom: 1px solid var(--line-e); gap: 9px; align-items: flex-start">
            <span class="tag t-err" style="font-size: 9px; min-width: 26px; justify-content: center">{{ f.c }}</span>
            <div style="flex: 1"><div class="small" style="color: var(--tx-2)">{{ f.n }}</div><div class="small muted">{{ f.ex }}</div></div>
          </div>
          <div class="small muted" style="margin-top: 8px">服务侧失败属于确定性问题（规则写错、边界漏判），改代码就能修，不需要重跑评测集。</div>
        </template>
        <div v-else class="ws clean"><div class="ws-h"><span v-html="icon('check')" /> 验收全部通过</div></div>
      </div>

      <div style="margin-top: 18px">
        <div class="row" style="gap: 7px; margin-bottom: 8px; align-items: baseline">
          <span style="font-size: 15px; font-weight: 650; color: var(--tx-1)">③ Agent 侧判据</span>
          <span class="small muted">评测集 + 基线对比 · 和服务侧完全不同的判据</span>
        </div>
      </div>
      <div class="card">
        <div class="ch"><span v-html="icon('check')" /><h3>Agent 四闸</h3><span class="spacer" />
          <span class="tag" :class="agGates.filter((g) => g.ok).length === 4 ? 't-ok' : 't-err'">{{ agGates.filter((g) => g.ok).length }}/4</span>
        </div>
        <div class="row wrap" style="gap: 6px">
          <span
            v-for="g in agGates"
            :key="g.key"
            class="achip"
            :style="'border-color:' + (g.ok ? 'var(--line-ok)' : 'var(--line-err)')"
          >
            <b :style="{ color: g.ok ? 'var(--ok)' : 'var(--err)' }">{{ g.ok ? '✓' : '✕' }}</b> {{ g.label }}
            <span style="color: var(--tx-9); margin-left: 5px">{{ g.detail }}</span>
          </span>
        </div>
      </div>
    </template>

    <!-- Agent 侧：评测台 + 知识库准确度 -->
    <template v-if="kind !== 'pbc'">
      <EvalWorkbench :agent="agent" />
      <KbAccuracy :agent="agent" :kb="agent && agent.kb" />
      <div class="card" style="border-color: var(--line-purple2); background: var(--bg-purple-soft3)">
        <div class="ch"><span v-html="icon('robot')" /><h3>为什么 Agent 的判据和 PBC 完全不同</h3></div>
        <div class="small muted" style="line-height: 1.8">
          还原对比要有一个原型页面逐像素比；Agent 的输出是话术、判定、草稿，<b>没有像素可对</b>。
          所以这一段的骨架是评测台的骨架：数据集 → 实验运行 → 逐条判分 → 与基线比 → 失败归类。<br />
          看三件事就够：<b>通过率有没有掉</b>（对比基线）、<b>掉在哪几条</b>（失败聚类）、<b>代价涨了多少</b>（成本与延迟）。
        </div>
      </div>
    </template>

    <div v-if="kind === 'hybrid'" class="card" style="border-color: var(--line-accent); background: var(--bg-accent-soft)">
      <div class="ch"><span v-html="icon('alert')" /><h3>组合项的真正风险</h3></div>
      <div class="small muted" style="line-height: 1.85">
        服务侧是<b>确定性</b>的：规则写错，改完重跑那 22 条就完了。
        Agent 侧是<b>概率性</b>的：改一句提示词，35/38 可能变 33/38，而且失败原因分散在话术、边界、升级判断上。<br />
        所以组合项不能「服务验完就发，Agent 顺手带上」。两套判据、两个负责人、同一次 tag——这是它比纯 PBC 多出来的成本。
      </div>
    </div>
  </div>
</template>
