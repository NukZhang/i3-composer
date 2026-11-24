<script setup>
// ProtoAgentReview —— Agent 单元的原型屏：质检复核（原型 v2.4 / v2.6）。
//
// 「交付物是界面，不是过程」：Agent 交付的是一屏工具——
// 它的产出（判定）在这里被人看、被人改、被人拍板。就这一屏，没有别的。
//
// 列表取自账本里的评测用例（每条用例就是一次真实会话的判定），
// 所以这一屏看到的是真的跑过的结果，不是编出来的演示数据。
import { computed, ref } from 'vue';
import { icon } from '../icons.js';

const props = defineProps({
  agent: { type: Object, default: null },
});

const rows = computed(() => {
  const cs = (props.agent && props.agent.cases) || [];
  return cs.map((c, i) => ({
    i,
    id: c.v,
    m: c.in,
    lv: c.ok ? (c.score >= 0.9 ? 'ok' : 'warn') : 'risk',
    t: c.ok ? (c.score >= 0.9 ? '已通过' : '需注意') : '必须复核',
    out: c.out,
    exp: c.exp,
    why: c.why,
    score: c.score,
  }));
});

const cur = ref(0);
function pick(i) {
  cur.value = i;
}

function lvCls(l) {
  return l === 'ok' ? 't-ok' : l === 'warn' ? 't-warn' : 't-err';
}
</script>

<template>
  <div v-if="!rows.length" class="card"><div class="empty">这个 Agent 还没有评测用例，没有可复核的会话。</div></div>
  <div v-else style="border: 1px solid var(--line-b); border-radius: 9px; overflow: hidden; background: var(--bg-panel)">
    <div class="row" style="gap: 12px; padding: 11px 14px; border-bottom: 1px solid var(--line-e); align-items: center">
      <h3 style="font-size: 13px; font-weight: 600; margin: 0">质检复核</h3>
      <span class="tag t-ok" style="font-size: 9px">Agent 已判 {{ rows.length }} 条 · 待你复核 {{ rows.filter((r) => r.lv === 'risk').length }} 条</span>
      <span class="spacer" style="flex: 1" />
      <span class="small muted">判为风险 {{ rows.filter((r) => !r.ok).length }} 条</span>
    </div>
    <div style="display: grid; grid-template-columns: 268px 1fr; min-height: 380px">
      <div style="border-right: 1px solid var(--line-e); overflow: auto">
        <div
          v-for="r in rows"
          :key="r.i"
          class="row"
          style="gap: 0; align-items: stretch; padding: 0; border-bottom: 1px solid var(--line-e); cursor: pointer"
          @click="pick(r.i)"
        >
          <div :style="'width:3px;background:' + (cur === r.i ? 'var(--accent)' : 'transparent')" />
          <div style="flex: 1; padding: 9px 12px">
            <div style="font-size: 12.5px; font-weight: 600">{{ r.id }}</div>
            <div class="small muted" style="margin-top: 2px">{{ r.m }}</div>
            <span class="tag" :class="lvCls(r.lv)" style="font-size: 9px; margin-top: 4px; display: inline-block">{{ r.t }}</span>
          </div>
        </div>
      </div>
      <div style="padding: 15px 18px">
        <div class="card" style="margin: 0 0 11px">
          <div class="ch" style="padding: 0 0 9px">
            <span v-html="icon('chat', 13)" /><h3 style="font-size: 13px">会话原文</h3>
            <span class="spacer" />
            <span class="tag" :class="lvCls(rows[cur].lv)" style="font-size: 9px">{{ rows[cur].t }}</span>
          </div>
          <div class="small muted">客户</div>
          <div class="pv" style="background: var(--bg-inset); border: 1px solid var(--line-f); border-radius: 8px; padding: 7px 11px; margin: 4px 0 8px; font-size: 12px; line-height: 1.7">
            {{ rows[cur].m }}
          </div>
          <div class="small muted" style="text-align: right">{{ agent && agent.n }}</div>
          <div class="pv" style="background: var(--bg-accent-soft); border: 1px solid var(--line-accent); border-radius: 8px; padding: 7px 11px; margin: 4px 0 0 40px; font-size: 12px; line-height: 1.7">
            {{ rows[cur].out }}
          </div>
        </div>
        <div class="card" style="margin: 0">
          <div class="ch" style="padding: 0 0 9px"><span v-html="icon('check', 13)" /><h3 style="font-size: 13px">Agent 判定依据</h3></div>
          <ul class="small" style="color: var(--tx-3); line-height: 1.85; margin: 0; padding-left: 18px">
            <li>{{ rows[cur].why }}</li>
            <li>期望：{{ rows[cur].exp }}　·　judge {{ rows[cur].score.toFixed(2) }}</li>
          </ul>
          <div class="row" style="gap: 7px; margin-top: 11px">
            <button class="btn small primary">接受判定</button>
            <button class="btn small">推翻并标注</button>
            <button class="btn small" style="color: var(--err)">转人工复核</button>
            <span class="small muted" style="margin-left: auto; align-self: center">Agent 只判不罚，处罚由人决定</span>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
