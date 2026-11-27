<script setup>
// EvalWorkbench —— Agent 的评测台（原型 v2.6 的 renderEvalWorkbench）。
//
// 骨架：数据集 → 实验运行 → 逐条判分 → 与基线比 → 失败归类。
// 组件只展示服务端给的评测事实；通过率能不能发布由 /api 的 gates 判定。
import { computed } from 'vue';
import { S } from '../store.js';
import { icon } from '../icons.js';
import { evPct, evRate, evCur, evBase, scoreColor, scoreBg } from '../derive.js';

const props = defineProps({
  agent: { type: Object, default: null },
});

const cur = computed(() => evCur(props.agent));
const base = computed(() => evBase(props.agent));
const dRate = computed(() => (cur.value && base.value ? (evRate(cur.value) - evRate(base.value)) * 100 : 0));
const avgScore = computed(() => {
  const cs = (props.agent && props.agent.cases) || [];
  return cs.length ? cs.reduce((a, x) => a + x.score, 0) / cs.length : 0;
});

function setEvView(v) {
  S.evView = v;
}
function toggleRow(i) {
  S.evRow = S.evRow === i ? null : i;
}
</script>

<template>
  <div v-if="!agent" class="card"><div class="empty">没有评测数据</div></div>
  <template v-else>
    <div class="ev-metrics">
      <div class="ev-m">
        <div class="ev-k">通过率</div>
        <div class="ev-v" :style="{ color: dRate >= 0 ? 'var(--ok)' : 'var(--err)' }">{{ evPct(evRate(cur)) }}</div>
        <div class="ev-s">{{ dRate >= 0 ? '▲ +' : '▼ ' }}{{ dRate.toFixed(1) }}pp vs 基线</div>
      </div>
      <div class="ev-m">
        <div class="ev-k">判官均分</div>
        <div class="ev-v">{{ avgScore.toFixed(2) }}</div>
        <div class="ev-s">展示样本均值</div>
      </div>
      <div class="ev-m">
        <div class="ev-k">总成本</div>
        <div class="ev-v" style="color: var(--warn)">{{ (cur && cur.cost) || '—' }}</div>
        <div class="ev-s">本轮累计</div>
      </div>
      <div class="ev-m">
        <div class="ev-k">P95 延迟</div>
        <div class="ev-v">{{ (cur && cur.p95) || '—' }}</div>
        <div class="ev-s">P50 {{ (cur && cur.p50) || '—' }}</div>
      </div>
      <div class="ev-m">
        <div class="ev-k">token</div>
        <div class="ev-v">{{ (cur && cur.tok) || '—' }}</div>
        <div class="ev-s">本轮累计</div>
      </div>
    </div>

    <div class="card">
      <div class="ch"><span v-html="icon('git')" /><h3>实验对比</h3><span class="spacer" /><span class="small muted">基线可切换 · 退步会标红</span></div>
      <div class="row wrap" style="gap: 9px; align-items: stretch">
        <div class="ev-exp">
          <div class="ev-tag">本轮</div>
          <div class="mono small" style="color: var(--info)">{{ cur && cur.id }}</div>
          <div style="font-size: 13px; font-weight: 600; color: var(--tx-1)">v{{ cur && cur.ver }}</div>
          <div class="small muted">{{ cur && cur.at }} · {{ cur && cur.pass }}/{{ cur && cur.total }}</div>
        </div>
        <div class="ev-vs">vs</div>
        <div class="ev-exp base">
          <div class="ev-tag">基线</div>
          <div class="mono small" style="color: var(--tx-9)">{{ base && base.id }}</div>
          <div style="font-size: 13px; font-weight: 600; color: var(--tx-2)">v{{ base && base.ver }}</div>
          <div class="small muted">{{ base && base.at }} · {{ base && base.pass }}/{{ base && base.total }}</div>
        </div>
        <div style="flex: 1; min-width: 150px">
          <div class="ev-trend">
            <div
              v-for="r in (agent.runs || []).slice().reverse()"
              :key="r.id"
              class="ev-bar"
              :title="r.id + ' ' + r.pass + '/' + r.total"
              :style="'height:' + Math.max(8, Math.round(evRate(r) * 100 * 0.42)) + 'px;background:' + (r === cur ? 'var(--accent)' : 'var(--line-i)')"
            />
          </div>
          <div class="small muted" style="margin-top: 3px">近 {{ (agent.runs || []).length }} 次运行通过率</div>
        </div>
      </div>
    </div>

    <div class="card">
      <div class="ch"><span v-html="icon('list')" /><h3>用例结果</h3><span class="spacer" />
        <span class="small muted">共 {{ agent.eval[1] }} 条 · 失败 {{ agent.eval[1] - agent.eval[0] }} 条（失败优先展示）</span>
      </div>
      <div class="row" style="gap: 5px; margin-bottom: 8px">
        <button v-for="v in [['compact', '紧凑'], ['full', '完整'], ['diff', '差异']]" :key="v[0]" class="btn small" :class="{ primary: S.evView === v[0] }" @click="setEvView(v[0])">{{ v[1] }}</button>
        <span class="spacer" style="flex: 1" /><span class="small muted">热力：分数越低越红 · 点行展开判官理由与轨迹</span>
      </div>
      <div class="ev-scroll">
        <table class="tb">
          <thead v-if="S.evView === 'compact'"><tr><th>用例</th><th style="width: 64px">judge</th><th style="width: 58px">耗时</th><th style="width: 54px">token</th></tr></thead>
          <thead v-else-if="S.evView === 'diff'"><tr><th style="width: 132px">用例</th><th>期望</th><th>实际输出</th><th style="width: 56px">judge</th></tr></thead>
          <thead v-else><tr><th style="width: 120px">用例</th><th style="width: 150px">输入</th><th>输出</th><th style="width: 112px">期望</th><th style="width: 52px">judge</th><th style="width: 50px">耗时</th></tr></thead>
          <tbody>
            <template v-for="(c, i) in agent.cases" :key="i">
              <tr class="ev-row" :class="{ bad: !c.ok }" @click="toggleRow(i)">
                <template v-if="S.evView === 'compact'">
                  <td class="small"><span v-if="!c.ok" style="color: var(--err)">✕ </span>{{ c.v }}</td>
                  <td><span class="ev-score" :style="'color:' + scoreColor(c.score) + ';background:' + scoreBg(c.score)">{{ c.score.toFixed(2) }}</span></td>
                  <td class="mono small muted">{{ c.ms }}</td><td class="mono small muted">{{ c.tok }}</td>
                </template>
                <template v-else-if="S.evView === 'diff'">
                  <td class="small">{{ c.v }}</td>
                  <td class="small" style="color: var(--tx-6)">{{ c.exp }}</td>
                  <td class="small" :style="{ color: c.ok ? 'var(--tx-3)' : 'var(--err-3)' }">{{ c.out }}</td>
                  <td><span class="ev-score" :style="'color:' + scoreColor(c.score) + ';background:' + scoreBg(c.score)">{{ c.score.toFixed(2) }}</span></td>
                </template>
                <template v-else>
                  <td class="small"><b style="font-weight: 550">{{ c.v }}</b></td>
                  <td class="small muted">{{ c.in }}</td>
                  <td class="small" style="color: var(--tx-3)">{{ c.out }}</td>
                  <td class="small" style="color: var(--tx-7)">{{ c.exp }}</td>
                  <td><span class="ev-score" :style="'color:' + scoreColor(c.score) + ';background:' + scoreBg(c.score)">{{ c.score.toFixed(2) }}</span></td>
                  <td class="mono small muted">{{ c.ms }}</td>
                </template>
              </tr>
              <tr v-if="S.evRow === i" class="ev-x">
                <td colspan="6" style="background: var(--bg-note); padding: 0">
                  <div style="padding: 10px 12px">
                    <div class="row" style="gap: 7px; margin-bottom: 6px">
                      <span class="tag" :class="c.ok ? 't-ok' : 't-err'" style="font-size: 9px">{{ c.ok ? '通过' : '不过' }}</span>
                      <span class="small muted">judge {{ c.score.toFixed(2) }} · {{ c.ms }} · {{ c.tok }} token</span>
                    </div>
                    <div class="small" style="color: var(--tx-3); line-height: 1.75"><b style="color: var(--tx-6)">判官理由：</b>{{ c.why }}</div>
                    <!-- 当轮装配：失败要能分清「没召回」还是「进了答歪」。
                         取的是沉淀这条用例时那一轮的装配（c.assemblies），不是最新一轮。 -->
                    <template v-if="(c.assemblies || []).length">
                      <div class="small muted" style="margin: 8px 0 4px">当轮装配（这条用例当时读到的上下文）</div>
                      <div v-for="(asm, k) in c.assemblies" :key="'a' + k" class="tree-box" style="margin: 0 0 6px">
                        <div class="tree-line">
                          <span class="k" style="width: 104px">{{ asm.node }} · {{ asm.loaded.length }} 篇</span>
                          <span class="v">
                            省略 {{ asm.omitted.count }} 篇
                            <span v-if="asm.omitted.count" style="color: var(--warn)">（{{ asm.omitted.reason }}）</span>
                            <span style="color: var(--tx-12)"> · 预算 {{ (asm.budget.used * 100).toFixed(1) }}%</span>
                          </span>
                        </div>
                        <div v-for="(d, j) in asm.loaded" :key="'d' + j" class="tree-line">
                          <span class="k" style="width: 104px" />
                          <span class="v small">{{ d.lib }}/{{ d.doc }} <span style="color: var(--tx-12)">ver {{ d.ver || '未登记' }} · 命中 {{ d.score }}</span></span>
                        </div>
                        <div v-if="!asm.loaded.length" class="tree-line">
                          <span class="k" style="width: 104px" />
                          <span class="v small" style="color: var(--warn)">一篇都没载入（没召回）</span>
                        </div>
                      </div>
                    </template>
                    <div v-else class="small muted" style="margin: 8px 0 4px">
                      当轮装配：这条用例没有记录装配（沉淀时还没有 context.assembled 事实，不补今天的装配冒充当时）
                    </div>
                    <div class="small muted" style="margin: 8px 0 4px">这条的执行轨迹</div>
                    <div class="tree-box" style="margin: 0">
                      <div v-for="(t, j) in agent.trace" :key="j" class="tree-line">
                        <span class="k" style="width: 104px">{{ t.n }}</span>
                        <span class="v">{{ t.d }} <span style="color: var(--tx-12)">{{ t.ms }}ms · {{ t.tok }} tok</span></span>
                      </div>
                    </div>
                  </div>
                </td>
              </tr>
            </template>
          </tbody>
        </table>
      </div>
    </div>

    <div class="card">
      <div class="ch"><span v-html="icon('alert')" /><h3>失败聚类</h3><span class="spacer" />
        <span class="small muted">失败 {{ agent.eval[1] - agent.eval[0] }} 条，按原因归组</span>
      </div>
      <div v-if="!(agent.fails || []).length" class="ws clean"><div class="ws-h"><span v-html="icon('check')" /> 本轮没有失败用例</div></div>
      <template v-else>
        <div v-for="(f, i) in agent.fails" :key="i" class="row" style="padding: 7px 0; border-bottom: 1px solid var(--line-e); align-items: flex-start; gap: 9px">
          <span class="tag t-err" style="font-size: 9px; min-width: 26px; justify-content: center">{{ f.c }}</span>
          <div style="flex: 1"><div class="small" style="color: var(--tx-2)">{{ f.n }}</div><div class="small muted">{{ f.ex }}</div></div>
        </div>
        <div class="small muted" style="margin-top: 8px">失败不是「再跑一遍」，是归类后提到 backlog：改提示词 / 改规则 / 补数据，各自有验收人。</div>
      </template>
    </div>

    <div class="card">
      <div class="ch"><span v-html="icon('refresh')" /><h3>运行历史</h3><span class="spacer" /><span class="small muted">{{ (agent.runs || []).length }} 次 · 点任意一次可切为对比基线</span></div>
      <div class="ev-scroll">
        <table class="tb">
          <thead><tr><th style="width: 96px">实验</th><th style="width: 58px">版本</th><th style="width: 70px">通过</th><th style="width: 62px">成本</th><th style="width: 54px">P50</th><th style="width: 58px">P95</th><th style="width: 52px">token</th><th style="width: 70px">时间</th></tr></thead>
          <tbody>
            <tr v-for="r in agent.runs || []" :key="r.id">
              <td class="mono small">
                {{ r.id }}
                <span v-if="r === cur" class="tag t-accent" style="font-size: 8px">本轮</span>
                <span v-if="r.base" class="tag t-gray" style="font-size: 8px">基线</span>
              </td>
              <td class="mono small">v{{ r.ver }}</td>
              <td class="small" :style="{ color: r.pass === r.total ? 'var(--ok)' : 'var(--tx-2)' }">{{ r.pass }}/{{ r.total }}</td>
              <td class="mono small muted">{{ r.cost }}</td><td class="mono small muted">{{ r.p50 }}</td><td class="mono small muted">{{ r.p95 }}</td><td class="mono small muted">{{ r.tok }}</td>
              <td class="small muted">{{ r.at }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </template>
</template>
