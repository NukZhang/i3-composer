<script setup>
// ProtoDunConsole —— 组合单元的原型屏：催缴业务台（原型 v2.4 / v2.6）。
//
// 组合项交付的是一套业务系统：策略、任务、话术三块。
// 服务承担了大部分确定性工作（谁该催、几次、什么渠道），Agent 只在
// 「怎么说」这一环介入。只画审核页会把 90% 的系统藏起来。
//
// 话术草稿取自账本里的评测用例（Agent 真跑过的产出），
// 策略档位是服务侧的确定性规则。
import { computed, ref } from 'vue';
import { icon } from '../icons.js';

const props = defineProps({
  agent: { type: Object, default: null },
});

const TIERS = [
  ['1–3 天', '短信 + 小程序推送', '每 3 天 1 次', '不升级'],
  ['4–15 天', '短信 + 人工电话', '每 3 天 1 次', '第 2 次转人工'],
  ['16–30 天', '电话 + 书面函', '每 5 天 1 次', '主管复核'],
  ['30 天以上', '专人上门', '按案件', '法务评估'],
];

const drafts = computed(() => {
  const cs = (props.agent && props.agent.cases) || [];
  return cs.map((c) => ({
    n: c.v,
    m: c.in,
    lv: c.ok ? 'ok' : 'risk',
    t: c.ok ? '可发送' : '应转人工',
    txt: c.out,
    chk: [
      ['无减免承诺', !/减免/.test(c.out)],
      ['无投诉/法务词', !/投诉|法务|起诉/.test(c.out)],
      ['未超频次', true],
    ],
    note: c.why,
  }));
});

const tab = ref(0);
const cur = ref(0);

function lvCls(l) {
  return l === 'ok' ? 't-ok' : l === 'warn' ? 't-warn' : 't-err';
}
</script>

<template>
  <div style="border: 1px solid var(--line-b); border-radius: 9px; overflow: hidden; background: var(--bg-panel)">
    <div class="row" style="gap: 12px; padding: 11px 14px; border-bottom: 1px solid var(--line-e); align-items: center">
      <h3 style="font-size: 13px; font-weight: 600; margin: 0">催缴业务台</h3>
      <span class="small muted">策略引擎派发 · Agent 只负责话术</span>
      <span class="spacer" style="flex: 1" />
      <span class="small muted">待审话术 {{ drafts.filter((d) => d.lv !== 'ok').length }}</span>
    </div>
    <div class="row" style="gap: 2px; padding: 0 14px; border-bottom: 1px solid var(--line-e)">
      <button
        v-for="(n, i) in ['任务看板', '策略配置', '话术审核']"
        :key="n"
        class="ptab"
        :class="{ on: tab === i }"
        style="border: 0; background: none; border-bottom: 2px solid transparent; border-radius: 0; padding: 9px 10px; font-size: 12px"
        @click="tab = i; cur = 0"
      >{{ n }}</button>
    </div>

    <div style="padding: 14px 18px; min-height: 340px">
      <!-- ① 任务看板 -->
      <template v-if="tab === 0">
        <div class="card" style="margin: 0">
          <div class="ch"><span v-html="icon('list')" /><h3>今日派发 · 由策略引擎按逾期分档生成</h3><span class="spacer" />
            <span class="small muted">这一屏全是确定性逻辑：谁该催、第几次、走哪个渠道，由策略引擎算，Agent 不参与。</span>
          </div>
          <table class="tb">
            <thead><tr><th>用例</th><th>场景</th><th>状态</th></tr></thead>
            <tbody>
              <tr v-for="d in drafts" :key="d.n">
                <td class="small">{{ d.n }}</td>
                <td class="small muted">{{ d.m }}</td>
                <td><span class="tag" :class="lvCls(d.lv)" style="font-size: 9px">{{ d.t }}</span></td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>

      <!-- ② 策略配置 -->
      <template v-else-if="tab === 1">
        <div class="card" style="margin: 0 0 11px">
          <div class="ch"><span v-html="icon('layers')" /><h3>触达策略 · 按逾期分档</h3></div>
          <table class="tb">
            <thead><tr><th>档位</th><th>触达方式</th><th>间隔</th><th>升级规则</th></tr></thead>
            <tbody>
              <tr v-for="t in TIERS" :key="t[0]">
                <td class="small"><b>{{ t[0] }}</b></td>
                <td class="small">{{ t[1] }}</td>
                <td class="small">{{ t[2] }}</td>
                <td class="small">{{ t[3] }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <div class="card" style="margin: 0">
          <div class="ch"><span v-html="icon('lock')" /><h3>频次与红线</h3></div>
          <div class="small" style="color: var(--tx-3); line-height: 2">
            单客户 7 天内 ≤ <b>3 次</b> 触达<br />
            欠费 &gt; <b>5 万</b> 或高价值客户 → <b>转人工</b><br />
            客户有投诉记录 → <b>不自动催</b><br />
            Agent 生成话术：<span class="tag t-ok" style="font-size: 9px">已开启</span>　只出草稿，不发送
          </div>
        </div>
      </template>

      <!-- ③ 话术审核 -->
      <template v-else>
        <div style="display: grid; grid-template-columns: 240px 1fr; gap: 0; border: 1px solid var(--line-b); border-radius: 9px; overflow: hidden">
          <div>
            <div
              v-for="(d, i) in drafts"
              :key="d.n"
              style="padding: 8px 12px; border-bottom: 1px solid var(--line-e); cursor: pointer"
              :style="cur === i ? 'background:var(--bg-accent-soft);border-left:3px solid var(--accent)' : ''"
              @click="cur = i"
            >
              <div style="font-size: 12px; font-weight: 600">{{ d.n }}</div>
              <div class="small muted">{{ d.m }}</div>
              <span class="tag" :class="lvCls(d.lv)" style="font-size: 9px; margin-top: 3px; display: inline-block">{{ d.t }}</span>
            </div>
          </div>
          <div v-if="drafts.length" style="padding: 12px 16px">
            <div class="row" style="gap: 8px; align-items: baseline; margin-bottom: 8px">
              <h3 style="font-size: 12.5px; font-weight: 600; margin: 0">Agent 草稿</h3>
              <span class="tag" :class="lvCls(drafts[cur].lv)" style="font-size: 9px">{{ drafts[cur].t }}</span>
            </div>
            <div class="pv" style="font-size: 12.5px; line-height: 1.9; background: var(--bg-inset); border: 1px solid var(--line-f); border-radius: 8px; padding: 10px 12px">
              {{ drafts[cur].txt }}
            </div>
            <div style="font-size: 12.5px; font-weight: 600; margin-top: 12px">红线自检</div>
            <div v-for="(c, i) in drafts[cur].chk" :key="i" class="small" :style="{ color: c[1] ? 'var(--ok)' : 'var(--err)', padding: '3px 0' }">
              {{ c[1] ? '✓' : '✕' }} {{ c[0] }}
            </div>
            <div class="small muted" style="margin-top: 8px">{{ drafts[cur].note }}</div>
            <div class="row" style="gap: 7px; margin-top: 11px">
              <button class="btn small primary">通过并发送</button>
              <button class="btn small">改一改</button>
              <button class="btn small">退回重生成</button>
              <button class="btn small">转人工</button>
              <span class="small muted" style="margin-left: auto; align-self: center">Agent 不直接发送，发送这一步永远是人点的。</span>
            </div>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>
