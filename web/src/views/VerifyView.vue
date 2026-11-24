<script setup>
import { computed } from 'vue';
import PageHeader from '../components/PageHeader.vue';
import { S, unitKind, currentAgent, currentUnit, refresh, toast } from '../store.js';
import { api } from '../api.js';
import { icon } from '../icons.js';
import { kindCls, kindText, evPct, evRate, evCur, evBase, scoreColor, scoreBg } from '../derive.js';

const kind = computed(() => unitKind());
const agent = computed(() => currentAgent());
const unit = computed(() => currentUnit() || {});
const gates = computed(() => (S.state && S.state.gates) || { all: [] });
const svc = computed(() => (S.state && S.state.svcVerify) || { pass: 0, total: 0, fails: [] });

const pbcGates = computed(() => gates.value.pbc || []);
const agGates = computed(() => gates.value.agent || []);

const cur = computed(() => evCur(agent.value));
const base = computed(() => evBase(agent.value));
const dRate = computed(() => (cur.value && base.value ? (evRate(cur.value) - evRate(base.value)) * 100 : 0));
const avgScore = computed(() => {
  const cs = (agent.value && agent.value.cases) || [];
  return cs.length ? cs.reduce((a, x) => a + x.score, 0) / cs.length : 0;
});

const canRelease = computed(
  () => kind.value === 'hybrid'
    ? pbcGates.value.filter((g) => g.ok).length === 4 && agGates.value.filter((g) => g.ok).length === 4
    : gates.value.all.length > 0 && gates.value.all.every((g) => g.ok),
);

function setEvView(v) {
  S.evView = v;
}
function toggleRow(i) {
  S.evRow = S.evRow === i ? null : i;
}
async function setBaseline(runId) {
  // 纯账本操作：把某次运行设成对比基线。
  try {
    await api.agentBaseline(agent.value.id, runId);
    toast('已设为基线', 'ok');
  } catch (e) {
    toast(e.message, 'err');
  }
  await refresh();
}
</script>

<template>
  <PageHeader
    :title="kind === 'hybrid'
      ? '验证 <span class=&quot;small&quot; style=&quot;color:var(--accent-tx2)&quot;>服务 ' + svc.pass + '/' + svc.total + ' · Agent ' + (agent ? agent.eval[0] : 0) + '/' + (agent ? agent.eval[1] : 0) + '</span>'
      : kind === 'agent'
        ? '验证 <span class=&quot;mono&quot; style=&quot;color:var(--ok);font-size:13px&quot;>' + (agent ? agent.eval[0] : 0) + '/' + (agent ? agent.eval[1] : 0) + ' · ' + evPct(agent ? evRate(cur) : 0) + '</span>'
        : '验证 <span class=&quot;mono&quot; style=&quot;color:var(--ok);font-size:13px&quot;>HEAD ' + ((S.state && S.state.commits && S.state.commits[0] && S.state.commits[0].no) || '—') + '</span>'"
    :sub="kind === 'hybrid'
      ? '组合项要过两套判据：微服务看还原与验收，Agent 看评测集。任何一侧有红，这个 tag 就不能打。'
      : kind === 'agent'
        ? '这不是页面，没有还原对比也没有测试验收。判据是一组固定用例加一个判官：过不过、差在哪、比上一版好了还是坏了。'
        : '两种对象两套标准：PBC 验「跑起来像不像原型」+ 测试验收；Agent 验评测集。四闸全过才能进发布。'"
  />

  <div class="unitbar">
    <span class="tag" :class="kindCls(kind)" style="font-size: 9px">{{ kindText(kind) }}</span>
    <span class="muted">交付单元 {{ unit.name }} · 提交 {{ (S.state && S.state.commits && S.state.commits[0] && S.state.commits[0].no) || '—' }}</span>
  </div>

  <!-- 组合项：合并门禁 -->
  <template v-if="kind === 'hybrid'">
    <div class="card" :style="'border-color:' + (canRelease ? 'var(--line-ok)' : 'var(--line-err)')">
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
          : '不能发：' + (svc.pass === svc.total ? '' : '服务侧还有 ' + (svc.total - svc.pass) + ' 条验收未过；') + (agGates.find((g) => g.key === 'agent-eval' && !g.ok) ? 'Agent 评测未达 95%。' : '') + '组合项最怕的就是只验一半。' }}
      </div>
    </div>

    <div class="card">
      <div class="ch"><span v-html="icon('list')" /><h3>验收清单 · {{ unit.name }}</h3><span class="spacer" /><span class="tag" :class="svc.pass === svc.total ? 't-ok' : 't-err'">{{ svc.pass }}/{{ svc.total }}</span></div>
      <template v-if="svc.fails && svc.fails.length">
        <div v-for="(f, i) in svc.fails" :key="i" class="row" style="padding: 7px 0; border-bottom: 1px solid var(--line-e); gap: 9px; align-items: flex-start">
          <span class="tag t-err" style="font-size: 9px; min-width: 26px; justify-content: center">{{ f.c }}</span>
          <div style="flex: 1"><div class="small" style="color: var(--tx-2)">{{ f.n }}</div><div class="small muted">{{ f.ex }}</div></div>
        </div>
        <div class="small muted" style="margin-top: 8px">服务侧失败属于确定性问题（规则写错、边界漏判），改代码就能修，不需要重跑评测集。</div>
      </template>
      <div v-else class="ws clean"><div class="ws-h"><span v-html="icon('check')" /> 验收全部通过</div></div>
    </div>
  </template>

  <!-- 单侧四闸（PBC / Agent） -->
  <div v-else class="card">
    <div class="ch">
      <span v-html="icon('check')" /><h3>交付四闸 · {{ (S.state && S.state.commits && S.state.commits[0] && S.state.commits[0].no) || '—' }}</h3>
      <span class="spacer" />
      <span class="tag" :class="gates.all.filter((g) => g.ok).length === 4 ? 't-ok' : 't-warn'">{{ gates.all.filter((g) => g.ok).length }}/4 通过</span>
    </div>
    <div class="row wrap" style="gap: 6px">
      <span
        v-for="g in gates.all"
        :key="g.key"
        class="achip"
        :style="'border-color:' + (g.ok ? 'var(--line-ok)' : 'var(--line-err)')"
      >
        <b :style="{ color: g.ok ? 'var(--ok)' : 'var(--err)' }">{{ g.ok ? '✓' : '✕' }}</b> {{ g.label }}
        <span style="color: var(--tx-9); margin-left: 5px">{{ g.detail }}</span>
      </span>
      <span v-if="!gates.all.length" class="small muted">还没有提交，四闸无从判定。</span>
    </div>
  </div>

  <!-- 评测台（Agent / 组合） -->
  <template v-if="kind !== 'pbc' && agent">
    <div class="ev-metrics">
      <div class="ev-m"><div class="ev-k">通过率</div><div class="ev-v" :style="{ color: dRate >= 0 ? 'var(--ok)' : 'var(--err)' }">{{ evPct(evRate(cur)) }}</div><div class="ev-s">{{ dRate >= 0 ? '▲ +' : '▼ ' }}{{ dRate.toFixed(1) }}pp vs 基线</div></div>
      <div class="ev-m"><div class="ev-k">判官均分</div><div class="ev-v">{{ avgScore.toFixed(2) }}</div><div class="ev-s">展示样本均值</div></div>
      <div class="ev-m"><div class="ev-k">总成本</div><div class="ev-v" style="color: var(--warn)">{{ (cur && cur.cost) || '—' }}</div><div class="ev-s">本轮累计</div></div>
      <div class="ev-m"><div class="ev-k">P95 延迟</div><div class="ev-v">{{ (cur && cur.p95) || '—' }}</div><div class="ev-s">P50 {{ (cur && cur.p50) || '—' }}</div></div>
      <div class="ev-m"><div class="ev-k">token</div><div class="ev-v">{{ (cur && cur.tok) || '—' }}</div><div class="ev-s">本轮累计</div></div>
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
      <div class="row" style="margin-top: 9px; gap: 6px">
        <button class="btn small" :disabled="!base" @click="setBaseline(base.id)">把基线设为它</button>
        <span class="small muted">评测结果由执行器提交（POST /api/agents/{{ agent.id }}/eval）；本服务不代跑评测。</span>
      </div>
    </div>

    <div class="card">
      <div class="ch">
        <span v-html="icon('list')" /><h3>用例结果</h3><span class="spacer" />
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
      <div class="ch">
        <span v-html="icon('alert')" /><h3>失败聚类</h3><span class="spacer" />
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
              <td class="mono small muted">{{ r.cost }}</td><td class="mono small muted">{{ r.p50 }}</td>
              <td class="mono small muted">{{ r.p95 }}</td><td class="mono small muted">{{ r.tok }}</td>
              <td class="small muted">{{ r.at }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </template>

  <div class="card" style="border-color: var(--line-purple2); background: var(--bg-purple-soft3)">
    <div class="ch"><span v-html="icon('robot')" /><h3>为什么这一页和 PBC 完全不同</h3></div>
    <div class="small muted" style="line-height: 1.8">
      还原对比要有一个原型页面逐像素比；Agent 的输出是话术、判定、草稿，<b>没有像素可对</b>。
      所以这一页的骨架是评测台的骨架：数据集 → 实验运行 → 逐条判分 → 与基线比 → 失败归类。<br />
      看三件事就够：<b>通过率有没有掉</b>（对比基线）、<b>掉在哪几条</b>（失败聚类）、<b>代价涨了多少</b>（成本与延迟）。
    </div>
    <button v-if="agent" class="btn small" style="margin-top: 9px" @click="S.agentId = agent.id; S.view = 'agent'"><span v-html="icon('robot', 11)" /> 打开 {{ agent.n }} 的定义与版本</button>
  </div>
</template>
