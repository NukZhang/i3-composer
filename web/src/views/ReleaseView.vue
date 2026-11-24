<script setup>
import { computed } from 'vue';
import PageHeader from '../components/PageHeader.vue';
import { S, unitKind, currentAgent, currentUnit, toast, refresh } from '../store.js';
import { api } from '../api.js';
import { icon } from '../icons.js';
import { kindCls, kindText, evCur, evBase, evPct, evRate } from '../derive.js';

const kind = computed(() => unitKind());
const agent = computed(() => currentAgent());
const unit = computed(() => currentUnit() || {});
const commits = computed(() => (S.state && S.state.commits) || []);
const deploys = computed(() => (S.state && S.state.deploys) || []);
const tags = computed(() => commits.value.filter((c) => c.tag));
const cur = computed(() => evCur(agent.value));
const base = computed(() => evBase(agent.value));
const pcts = [10, 50, 100];
const rollout = computed(() => (agent.value && agent.value.rollout) || 0);

async function publish() {
  try {
    const j = await api.agentPublish(agent.value.id);
    toast('评测通过，已注册 v' + j.ver + '（流量 10%）', 'ok');
  } catch (e) {
    toast(e.message, 'err');
  }
  await refresh();
}
async function rolloutTo(pct) {
  try {
    await api.agentRollout(agent.value.id, pct);
    toast('已放量到 ' + pct + '%', 'ok');
  } catch (e) {
    toast(e.message, 'err');
  }
  await refresh();
}
async function rollback() {
  try {
    const j = await api.agentRollback(agent.value.id);
    toast('已回滚' + (j.to ? '到 v' + j.to : '') + '，流量归零', 'warn');
  } catch (e) {
    toast(e.message, 'err');
  }
  await refresh();
}
function deployed(tag) {
  return deploys.value.filter((d) => d.tag === tag).slice(-1)[0];
}
</script>

<template>
  <PageHeader
    :title="kind === 'agent'
      ? '发布 <span class=&quot;mono&quot; style=&quot;color:var(--purple);font-size:13px&quot;>' + (agent && agent.n) + ' v' + (agent && agent.ver) + '</span>'
      : '发布 <span class=&quot;small&quot; style=&quot;color:var(--info)&quot;>当前主线</span>'"
    :sub="kind === 'agent'
      ? 'Agent 的发布＝把 actor 类型指向新 digest，再按流量分档放量。不是「改个提示词立刻全量生效」。'
      : '交付 = 把 tag 指向的那棵完整树部署出去；回滚 = 重新部署旧 tag，不改历史。'"
  >
    <button class="btn small" @click="S.view = 'plan'"><span v-html="icon('git', 12)" /> 回方案页</button>
  </PageHeader>

  <div class="unitbar">
    <span class="tag" :class="kindCls(kind)" style="font-size: 9px">{{ kindText(kind) }}</span>
    <span class="muted">{{ unit.name }} · 已交付 {{ (unit.tags || []).join(' / ') || '—' }}</span>
  </div>

  <!-- Agent 发布：注册 + 灰度 -->
  <template v-if="kind === 'agent'">
    <div class="card">
      <div class="ch">
        <span v-html="icon('cloud')" /><h3>灰度放量</h3><span class="spacer" />
        <span class="tag" :class="rollout >= 100 ? 't-ok' : 't-warn'">当前 {{ rollout }}% 流量</span>
      </div>
      <div class="row" style="gap: 6px; margin-bottom: 10px">
        <div
          v-for="(p, i) in pcts"
          :key="p"
          class="ev-exp"
          :style="'flex:1;min-width:0;' + (p === rollout ? 'border-color:var(--accent);background:var(--bg-accent-soft)' : '') + (p < rollout ? ';opacity:.55' : '')"
        >
          <div class="ev-tag">第 {{ i + 1 }} 档</div>
          <div :style="'font-size:16px;font-weight:650;color:' + (p === rollout ? 'var(--accent-tx2)' : 'var(--tx-2)')">{{ p }}%</div>
          <div class="small muted">{{ p < rollout ? '已通过' : p === rollout ? '观测中' : '未开始' }}</div>
        </div>
      </div>
      <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 9px">
        <div class="card tight" style="margin: 0; background: var(--bg-panel)">
          <div class="small muted">评测集通过率</div>
          <div style="font-size: 15px; font-weight: 650; color: var(--ok)">{{ cur ? evPct(evRate(cur)) : '—' }} 通过</div>
          <div class="small muted">{{ cur ? cur.pass + '/' + cur.total : '—' }} 条 · 与评测集口径一致</div>
        </div>
        <div class="card tight" style="margin: 0; background: var(--bg-panel)">
          <div class="small muted">转人工</div>
          <div style="font-size: 15px; font-weight: 650; color: var(--warn)">{{ ((agent && agent.fails) || []).reduce((a, f) => a + f.c, 0) }} 条</div>
          <div class="small muted">失败聚类命中 · 均未自动发送</div>
        </div>
      </div>
      <div class="row" style="margin-top: 10px; gap: 6px">
        <button v-if="!agent.published" class="btn primary small" @click="publish"><span v-html="icon('cloud', 11)" /> 发布新版本（v{{ agent.ver }}）</button>
        <button v-else-if="rollout < 100" class="btn primary small" @click="rolloutTo(rollout === 10 ? 50 : 100)">
          <span v-html="icon('chev', 11)" /> 放量到 {{ rollout === 10 ? 50 : 100 }}%
        </button>
        <button v-else class="btn primary small" disabled>已全量</button>
        <button class="btn small" :disabled="!agent.published" @click="rollback"><span v-html="icon('refresh', 11)" /> 回滚到上一版</button>
        <span class="spacer" style="flex: 1" />
        <span class="small muted">每档至少观察 24h；线上抽样不达标自动退回上一档</span>
      </div>
    </div>

    <div class="card">
      <div class="ch"><span v-html="icon('git')" /><h3>本版 vs 上一版</h3><span class="spacer" /><span class="mono small">v{{ base && base.ver }} → v{{ cur && cur.ver }}</span></div>
      <table class="tb">
        <thead><tr><th>指标</th><th style="width: 110px">上一版</th><th style="width: 110px">本版</th><th style="width: 90px">变化</th></tr></thead>
        <tbody>
          <tr v-if="cur && base">
            <td class="small">评测通过</td>
            <td class="mono small muted">{{ base.pass }}/{{ base.total }}</td>
            <td class="mono small">{{ cur.pass }}/{{ cur.total }}</td>
            <td class="small" :style="{ color: cur.pass - base.pass >= 0 ? 'var(--ok)' : 'var(--err)' }">{{ (cur.pass - base.pass >= 0 ? '+' : '') + (cur.pass - base.pass) }}</td>
          </tr>
          <tr v-if="cur && base">
            <td class="small">成本</td><td class="mono small muted">{{ base.cost }}</td><td class="mono small">{{ cur.cost }}</td>
            <td class="small" style="color: var(--warn)">—</td>
          </tr>
          <tr v-if="cur && base">
            <td class="small">P95 延迟</td><td class="mono small muted">{{ base.p95 }}</td><td class="mono small">{{ cur.p95 }}</td>
            <td class="small" style="color: var(--warn)">—</td>
          </tr>
        </tbody>
      </table>
      <div class="small muted" style="margin-top: 8px">通过率与代价的取舍要人来拍，不该由 Agent 自己决定。</div>
    </div>

    <div class="card">
      <div class="ch"><span v-html="icon('tag')" /><h3>版本线</h3><span class="spacer" /><span class="small muted">保留 3 个版本可回滚</span></div>
      <div class="tree-box">
        <div class="tree-line"><span class="k">v{{ agent.ver }}</span><span class="v">当前 · 评测 {{ agent.eval[0] }}/{{ agent.eval[1] }} · tag {{ (tags[0] && tags[0].tag) || '—' }} · {{ agent.published ? '已注册 agent/' + agent.id : '未发布' }}</span></div>
        <div class="tree-line"><span class="k">v{{ base && base.ver }}</span><span class="v">上一版 · 回滚目标</span></div>
        <div v-if="agent.rollbackTo" class="tree-line"><span class="k">回滚</span><span class="v">已回滚到 v{{ agent.rollbackTo }}，流量归零</span></div>
      </div>
    </div>

    <div class="card" style="border-color: var(--line-purple2); background: var(--bg-purple-soft3)">
      <div class="ch"><span v-html="icon('robot')" /><h3>为什么 Agent 也要灰度</h3></div>
      <div class="small muted" style="line-height: 1.8">
        评测集是抽样的，不可能覆盖所有客户和所有说法。本版通过率涨了，但仍可能在某个客群上翻车——所以先给 10% 流量，看线上抽样，再决定放不放。<br />
        微服务的灰度看错误率和延迟；Agent 的灰度看<b>通过率、转人工率、人工改写率</b>——这三条才是它的「健康指标」。
      </div>
    </div>
  </template>

  <!-- PBC / 组合：tag 交付 -->
  <template v-else>
    <div class="card">
      <div class="ch"><span v-html="icon('tag')" /><h3>交付记录（tag）</h3><span class="spacer" /><span class="small muted">{{ tags.length }} 个</span></div>
      <div v-if="!tags.length" class="small muted">还没有 tag。先在方案页看提交与四闸，SA 审批后由 workflow 打 tag。</div>
      <template v-for="c in tags" :key="c.tag">
        <div class="row" style="padding: 9px 0; border-bottom: 1px solid var(--line-e)">
          <span class="tagname" style="font-size: 11px">{{ c.tag }}</span>
          <span class="mono small" style="color: var(--accent-tx2)">{{ c.no }}</span>
          <span class="small muted" style="flex: 1">{{ c.msg }}</span>
          <span class="tag" :class="deployed(c.tag) ? 't-ok' : 't-gray'">{{ deployed(c.tag) ? '已部署' : '未部署' }}</span>
        </div>
        <div v-if="deployed(c.tag)" class="small muted" style="padding: 0 0 8px 0">{{ deployed(c.tag).url }} · {{ deployed(c.tag).at }} · {{ deployed(c.tag).status }}</div>
      </template>
    </div>

    <div class="card">
      <div class="ch"><span v-html="icon('cloud')" /><h3>部署记录</h3></div>
      <div v-if="!deploys.length" class="small muted">暂无记录。部署由 workflow 在发布后执行，这里显示真实事实。</div>
      <table v-else class="tb">
        <thead><tr><th>tag</th><th>提交</th><th>地址</th><th>时间</th><th>状态</th></tr></thead>
        <tbody>
          <tr v-for="(d, i) in deploys.slice().reverse()" :key="i">
            <td class="tagname">{{ d.tag || '—' }}</td><td class="mono small">{{ d.commit }}</td>
            <td class="small" style="color: var(--info)">{{ d.url }}</td><td class="small muted">{{ d.at }}</td>
            <td><span class="tag t-ok">{{ d.status }}</span></td>
          </tr>
        </tbody>
      </table>
    </div>

    <div v-if="kind === 'hybrid' && agent" class="card" style="border-color: var(--line-accent)">
      <div class="ch"><span v-html="icon('robot')" /><h3>Agent 侧 · 同一次发布</h3><span class="spacer" /><span class="mono small" style="color: var(--info)">{{ agent.n }} v{{ agent.ver }}</span></div>
      <div class="tree-box">
        <div class="tree-line"><span class="k">v{{ agent.ver }}</span><span class="v">actor 指向 {{ agent.digest }} · 评测 {{ agent.eval[0] }}/{{ agent.eval[1] }}</span></div>
        <div class="tree-line"><span class="k">锁定</span><span class="v">tag {{ (tags[0] && tags[0].tag) || '—' }} 同时锁镜像 digest 与 Agent digest</span></div>
      </div>
      <div class="small muted" style="margin-top: 8px">组合项的 tag 是「一份交付物」，不是一个服务 + 一个附赠。回滚要一起回——只回服务不回 Agent，等于把旧话术套在新策略上。</div>
    </div>
  </template>
</template>
