<script setup>
// OrchView —— 项目级「发布编排」（原型 v2.8–v3.2）。
//
// 单元只做到「可以交付」：四阶段走完、打 tag 进池。上哪个环境、和谁一起上，
// 在这里定。三条硬规则写在页面上，也写在账本里：
//   1. 勾选的最小单位是「一次 tag」，不是「服务」或「Agent」；
//   2. 批次锁住勾选那批的 tag 与 digest，生成后不可改；
//   3. 回滚只能整批——只回一半会留下说不清的组合。
import { computed, ref, watch } from 'vue';
import PageHeader from '../components/PageHeader.vue';
import { S, currentProject, toast, refresh } from '../store.js';
import { api } from '../api.js';
import { icon } from '../icons.js';

const data = ref(null);
const sel = ref({});
const env = ref('SIT');
const mode = ref('full');
const filter = ref('all');
const preview = ref(false);
const busy = ref(false);
const can = (a) => (S.acts || []).includes(a);

function kindText(k) {
  return k === 'agent' ? 'Agent' : 'PBC';
}
function kindCls(k) {
  return k === 'agent' ? 't-purple' : 't-info';
}

async function load() {
  if (!S.projId) return;
  try {
    data.value = await api.orch(S.projId);
    const o = {};
    for (const u of data.value.units) o[u.id] = true; // 默认全选
    sel.value = o;
    if (data.value.envs && data.value.envs.length && !data.value.envs.includes(env.value)) env.value = data.value.envs[0];
  } catch (e) {
    toast('拉取编排失败：' + e.message, 'err');
    data.value = null;
  }
}
watch(
  () => [S.projId, S.view, S.tick],
  () => {
    if (S.view === 'orch') load();
  },
  { immediate: true },
);

const units = computed(() => (data.value && data.value.units) || []);
const filtered = computed(() =>
  filter.value === 'all' ? units.value : units.value.filter((u) => u.kind === filter.value),
);
const counts = computed(() => ({
  all: units.value.length,
  pbc: units.value.filter((u) => u.kind === 'pbc').length,
  agent: units.value.filter((u) => u.kind === 'agent').length,
}));
const selected = computed(() => units.value.filter((u) => sel.value[u.id]));
const selectedReady = computed(() => selected.value.filter((u) => u.ready).length);
const blocked = computed(() => selected.value.filter((u) => !u.ready));
const unselected = computed(() => units.value.filter((u) => !sel.value[u.id]));
const canRelease = computed(() => selected.value.length > 0 && blocked.value.length === 0);

// 批次状态：账本里是什么就是什么；failed / superseded 不再被画成绿色「已上线」。
function batchText(b) {
  // active 只代表「账本上是这一批在上」，不代表进程还活着——是否在线由 live 标签单独说（#29）。
  return ({ active: '已发布', rolledback: '已回滚', failed: '发布失败' })[b.status] || b.status;
}
function batchCls(b) {
  return ({ active: 't-ok', rolledback: 't-err', failed: 't-err' })[b.status] || 't-gray';
}
// 只有 active 且没回滚的批次还能「整批回滚」；失败批次已收过进程，不需要再回滚。
function canRollback(b) {
  return b.status === 'active' || b.status === 'failed';
}
function hasURL(b) {
  return (b.units || []).some((u) => u.url);
}
// live 是编排接口现场探的 /healthz：账本说发过，进程在不在是另一回事（#29）。
function liveOf(b) {
  const i = ((data.value && data.value.batches) || []).findIndex((x) => x.id === b.id);
  return i >= 0 && !!((data.value.live || [])[i]);
}

function toggle(u) {
  sel.value[u.id] = !sel.value[u.id];
}
function selectKinds() {
  const o = {};
  for (const u of units.value) o[u.id] = filter.value === 'all' || u.kind === filter.value;
  sel.value = o;
}

async function doRelease() {
  if (!canRelease.value) return;
  preview.value = false;
  busy.value = true;
  try {
    const b = await api.release(S.projId, { env: env.value, mode: mode.value, units: selected.value.map((u) => u.id) });
    toast('批次 ' + b.id + ' 已发布到 ' + b.env + ' · 锁定 ' + b.units.length + ' 个 tag', 'ok');
    await load();
    refresh();
  } catch (e) {
    toast(e.message, 'err');
  } finally {
    busy.value = false;
  }
}

async function rollback(b) {
  if (!window.confirm('整批回滚 ' + b.id + '？批次是发布单位，不能只回一半。')) return;
  busy.value = true;
  try {
    await api.rollback(S.projId, b.id);
    toast('批次 ' + b.id + ' 已整批回滚', 'ok');
    await load();
    refresh();
  } catch (e) {
    toast(e.message, 'err');
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <PageHeader
    title="发布编排"
    sub="单元只做到可以交付；上哪个环境、和谁一起上，在这里定。勾选的最小单位是一次 tag，批次锁住 tag 与 digest，只能整批回滚。"
  >
    <span class="tag t-info" style="font-size: 9px">{{ currentProject() ? currentProject().name : '—' }}</span>
  </PageHeader>

  <div v-if="!data" class="card"><div class="empty">正在读取编排…</div></div>

  <template v-else>
    <div class="card" style="margin-bottom: 9px">
      <div class="ch">
        <span v-html="icon('layers')" /><h3>编排链</h3>
        <span class="spacer" />
        <span class="small muted">已选 {{ selected.length }}/{{ units.length }} 个单元 · {{ selectedReady }} 就绪</span>
      </div>

      <div class="row wrap" style="gap: 6px; margin-bottom: 10px">
        <button class="btn small" :class="{ primary: filter === 'all' }" @click="filter = 'all'; selectKinds()">全部 {{ counts.all }}</button>
        <button v-if="counts.pbc" class="btn small" :class="{ primary: filter === 'pbc' }" @click="filter = 'pbc'; selectKinds()">只 PBC {{ counts.pbc }}</button>
        <button v-if="counts.agent" class="btn small" :class="{ primary: filter === 'agent' }" @click="filter = 'agent'; selectKinds()">只 Agent {{ counts.agent }}</button>
        <span class="spacer" style="flex: 1" />
        <span class="small muted">点卡片 = 勾选 / 取消；未勾选的不参与这一批</span>
      </div>

      <div class="orch-chain">
        <template v-for="(u, i) in filtered" :key="u.id">
          <div class="orch-card" :class="{ off: !sel[u.id], ready: u.ready }" @click="toggle(u)">
            <div class="row" style="gap: 6px; align-items: center">
              <span class="orch-box">{{ sel[u.id] ? '☑' : '□' }}</span>
              <span class="tag" :class="kindCls(u.kind)" style="font-size: 9px">{{ kindText(u.kind) }}</span>
              <span class="spacer" />
              <span v-if="!sel[u.id]" class="small muted">未勾选</span>
            </div>
            <div style="font-size: 12px; font-weight: 600; color: var(--tx-2); margin: 5px 0 3px">{{ u.name }}</div>
            <div class="small" :style="{ color: u.ready ? 'var(--ok)' : 'var(--warn)' }">
              {{ u.ready ? (u.tag + ' 已进池') : (u.reason || '未就绪') }}
            </div>
            <div class="small muted mono" style="margin-top: 2px">{{ u.head || '—' }}</div>
            <div class="row" style="gap: 5px; margin-top: 6px">
              <button class="btn small" @click.stop="S.pbcId = u.id; S.view = 'verify'">去看看</button>
            </div>
          </div>
          <span v-if="i < filtered.length - 1" class="orch-arrow">→</span>
        </template>
      </div>
      <div v-if="!filtered.length" class="small muted">这个项目还没有交付单元。</div>
    </div>

    <div style="display: grid; grid-template-columns: 1fr 330px; gap: 9px; align-items: start">
      <div class="card" style="margin: 0">
        <div class="ch"><span v-html="icon('cloud')" /><h3>本批锁定</h3><span class="spacer" />
          <span class="small muted">生成后不可改</span>
        </div>
        <div v-if="!selected.length" class="small muted">一个单元都没勾。未勾选的单元不会以任何形式被带上。</div>
        <template v-else>
          <div v-for="u in selected" :key="u.id" class="row" style="padding: 5px 0; border-bottom: 1px solid var(--line-e); gap: 8px">
            <span class="tag" :class="kindCls(u.kind)" style="font-size: 9px">{{ kindText(u.kind) }}</span>
            <span style="font-size: 11.5px; flex: 1">{{ u.name }}</span>
            <span v-if="u.tag" class="mono small">{{ u.tag }}</span>
            <span v-else class="tag t-err" style="font-size: 9px">未打 tag</span>
            <span class="mono small muted">{{ u.digest ? u.digest.slice(0, 21) + '…' : '—' }}</span>
          </div>
        </template>
        <div v-if="unselected.length" class="small muted" style="margin-top: 8px">
          本批不动：{{ unselected.map((u) => u.name).join('、') }}——它们保持线上现状，不会被改动。
        </div>
      </div>

      <div class="card" style="margin: 0">
        <div class="ch"><span v-html="icon('alert')" /><h3>就绪检查</h3></div>
        <div class="row" style="gap: 6px; margin-bottom: 7px">
          <span class="small muted" style="width: 34px">环境</span>
          <select v-model="env" class="bug-sev" style="flex: 1; font-size: 11px">
            <option v-for="e in data.envs" :key="e">{{ e }}</option>
          </select>
        </div>
        <div class="row" style="gap: 6px; margin-bottom: 9px">
          <span class="small muted" style="width: 34px">方式</span>
          <select v-model="mode" class="bug-sev" style="flex: 1; font-size: 11px">
            <option value="full">全量</option>
            <!-- 灰度还没接（#23）：标成不可选并写明原因，别让人以为自己选了一个有区别的方式。 -->
            <option value="gray" disabled>灰度（还没接，请用全量）</option>
          </select>
        </div>
        <div class="small muted" style="margin: -3px 0 9px 40px">灰度按比例分批放量还没实现，现在只有全量。</div>
        <div v-for="u in selected" :key="'r-' + u.id" class="small" style="padding: 3px 0">
          <span :style="{ color: u.ready ? 'var(--ok)' : 'var(--err)' }">{{ u.ready ? '✓' : '✕' }}</span>
          {{ u.name }}
          <span class="muted">{{ u.ready ? (u.tag) : (u.reason || '未就绪') }}</span>
        </div>
        <button class="btn primary" style="width: 100%; margin-top: 10px" :disabled="!canRelease || !can('tag') || busy" @click="preview = true">
          <span v-html="icon('cloud', 12)" /> 发布本批次（{{ selected.length }} 个单元）
        </button>
        <div v-if="!selected.length" class="small muted" style="margin-top: 6px">一个单元都没勾。</div>
        <div v-else-if="blocked.length" class="small" style="margin-top: 6px; color: var(--err)">
          有 {{ blocked.length }} 个单元未就绪，整批不发——不会被「顺手带上」。
        </div>
        <div v-if="unselected.length" class="small muted" style="margin-top: 6px">
          未勾选的 {{ unselected.length }} 个单元保持线上现状，不会被改动。
        </div>
      </div>
    </div>

    <div class="card">
      <div class="ch"><span v-html="icon('git')" /><h3>历史批次</h3><span class="spacer" />
        <span class="small muted">{{ (data.batches || []).length }} 个</span>
      </div>
      <div v-if="!(data.batches || []).length" class="small muted">还没有发布过批次。</div>
      <div v-for="b in data.batches || []" :key="b.id" class="row wrap" style="padding: 7px 0; border-bottom: 1px solid var(--line-e); gap: 8px; align-items: center">
        <span class="mono small">{{ b.id }}</span>
        <span class="tag t-info" style="font-size: 9px">{{ b.env }}</span>
        <span class="tag t-gray" style="font-size: 9px" :title="b.mode === 'gray' ? '这是早期批次留下的记录：当时灰度没有真正分批放量' : ''">{{ b.mode === 'gray' ? '灰度（未开放）' : '全量' }}</span>
        <span class="small" style="flex: 1">{{ b.units.map((u) => u.name + ' ' + u.tag + (u.url ? ' · ' + u.url : ' · 定义')).join('；') }}</span>
        <!-- 状态只说账本里的结论；进程在不在由 live 单独说（#29：以前旧批次明明已经死了还写「已上线」） -->
        <span class="tag" :class="batchCls(b)" style="font-size: 9px">{{ batchText(b) }}</span>
        <span v-if="b.status === 'active' && hasURL(b)" class="tag" :class="liveOf(b) ? 't-ok' : 't-err'" style="font-size: 9px">{{ liveOf(b) ? '进程在线' : '进程不在' }}</span>
        <span v-if="b.reason" class="small" style="color: var(--err); flex: 1 0 100%">{{ b.reason }}</span>
        <span class="small muted">{{ b.by }} · {{ b.at }}</span>
        <button class="btn small" v-if="canRollback(b) && can('tag')" :disabled="busy" @click="rollback(b)">整批回滚</button>
      </div>
    </div>

    <div v-if="preview" class="mbox-bg" @click.self="preview = false">
      <div class="card mbox" style="width: 520px">
        <div class="ch"><h3>确认发布 · {{ env }}</h3></div>
        <div class="small muted" style="margin-bottom: 8px">这一批会锁住以下 tag 与 digest，生成后不可改；没勾的单元不在其中。</div>
        <div v-for="u in selected" :key="'p-' + u.id" class="row" style="gap: 8px; padding: 4px 0">
          <span class="tag" :class="kindCls(u.kind)" style="font-size: 9px">{{ kindText(u.kind) }}</span>
          <span style="flex: 1; font-size: 11.5px">{{ u.name }}</span>
          <span class="mono small">{{ u.tag }}</span>
          <span class="mono small muted">{{ u.digest.slice(0, 17) }}…</span>
        </div>
        <div class="row" style="gap: 6px; margin-top: 12px; justify-content: flex-end">
          <button class="btn small" @click="preview = false">取消</button>
          <button class="btn small primary" :disabled="busy" @click="doRelease">发布</button>
        </div>
      </div>
    </div>
  </template>
</template>

<style scoped>
.orch-chain { display: flex; align-items: stretch; gap: 4px; overflow-x: auto; padding-bottom: 4px; }
.orch-card { min-width: 170px; flex: 1; border: 1px solid var(--line-i); border-radius: 7px; padding: 8px 10px; cursor: pointer;
  background: var(--bg-el); transition: border-color .12s, opacity .12s; }
.orch-card.ready { border-color: color-mix(in srgb, var(--ok) 45%, transparent); }
.orch-card.off { border-style: dashed; opacity: .55; }
.orch-card:hover { border-color: var(--accent); }
.orch-box { font-size: 13px; color: var(--tx-6); }
.orch-arrow { align-self: center; color: var(--tx-11); font-size: 12px; }
.mbox-bg { position: fixed; inset: 0; background: rgba(0,0,0,.45); display: flex; align-items: center; justify-content: center; z-index: 50; }
</style>
