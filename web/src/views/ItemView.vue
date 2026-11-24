<script setup>
import { computed, ref, watch } from 'vue';
import PageHeader from '../components/PageHeader.vue';
import { S, refresh, toast } from '../store.js';
import { api } from '../api.js';
import { icon } from '../icons.js';

const item = computed(() => S.inbox.find((x) => x.pbc === S.pbcId && x.id === S.itemId) || S.inbox.find((x) => x.id === S.itemId) || null);
const detail = ref(null);
const err = ref('');

watch(
  () => [S.pbcId, S.itemId, S.state && S.state.seq],
  async () => {
    if (!S.itemId) return;
    try {
      detail.value = await api.item(S.itemId, S.pbcId);
      err.value = '';
    } catch (e) {
      detail.value = null;
      err.value = e.message;
    }
  },
  { immediate: true },
);

const facts = computed(() => (detail.value && detail.value.facts) || []);
const canBacklog = computed(() => (S.acts || []).includes('backlog'));
const canBuild = computed(() => (S.acts || []).includes('build'));
const canTag = computed(() => (S.acts || []).includes('tag'));

const note = ref('');
const busy = ref(false);

async function run() {
  busy.value = true;
  try {
    const j = await api.runItem(item.value.id, item.value.pbc);
    toast(item.value.id + ' 已发起（实例 ' + j.instance + '）', 'ok');
  } catch (e) {
    toast(e.message, 'err');
  }
  busy.value = false;
  await refresh();
}

async function decide(approve) {
  busy.value = true;
  try {
    await api.answer(item.value.id, item.value.pbc, approve, note.value);
    toast(approve ? item.value.id + ' 已提交发布决定' : '发布已拒绝', approve ? 'ok' : 'warn');
  } catch (e) {
    toast(e.message, 'err');
  }
  busy.value = false;
  await refresh();
}

function runText(run) {
  return { idea: '未发起', draft: '未发起', running: '执行中', waiting: '待发布', paused: '已介入', blocked: '受阻', published: '已发布' }[run] || run || '—';
}
function runCls(run) {
  return 'cr-state ' + (['published', 'running', 'waiting', 'paused', 'blocked'].includes(run) ? run : 'draft');
}
function stepCls(st) {
  return st === 'done' ? 'done' : st === 'doing' ? 'doing' : st === 'blocked' ? 'blocked' : st.human ? 'human' : '';
}
function stepMark(st) {
  return st === 'done' ? '✓' : st === 'doing' ? '·' : st === 'blocked' ? '!' : st.human ? '你' : '';
}
// 事实类型 → 人可以读懂的一句话。它只是给事实加注释，不新增任何状态。
function factText(f) {
  return {
    'item.created': '工作项创建',
    'item.run.started': '发起交付（workflow 已启动）',
    'item.step.done': '一步完成',
    'item.waiting': '到发布闸，等待拍板',
    'publish.decided': '人的发布决定',
    'item.published': '已发布（打 tag）',
    'item.blocked': '受阻',
    'tag.created': '打了 tag',
    'build.succeeded': '镜像构建完成',
    'deploy.succeeded': '部署完成',
    'artifact.drafted': '制品草稿落账',
    'artifact.accepted': '制品被接受',
    'commit.created': '原子提交',
    'annotation.added': '批注',
    'svc.verified': '服务验收结果',
    'unit.created': '交付单元登记',
    'brd.saved': 'BRD 保存',
  }[f.type] || f.type;
}
</script>

<template>
  <PageHeader
    :title="item ? item.id + ' · ' + item.t : '工作项'"
    :sub="item ? item.kind + ' · 类型 ' + item.type + ' · 模式 ' + (item.mode === 'fast' ? '直通（人只在发布前拍板）' : '介入（可随时改）') + ' · 目标 ' + item.env : '未选择'"
  >
    <button class="btn small" @click="S.view = 'projects'"><span v-html="icon('list', 12)" /> 回到需求池</button>
  </PageHeader>

  <div v-if="err" class="card" style="border-color: var(--line-err)"><div class="small" style="color: var(--err)">{{ err }}</div></div>
  <div v-else-if="!item" class="card"><div class="empty">这个工作项不在账本里（可能属于另一个交付单元）。</div></div>

  <template v-else>
    <div class="card" style="padding: 11px 13px">
      <div class="row">
        <span :class="runCls(item.run)">{{ runText(item.run) }}</span>
        <span class="small muted">
          <template v-if="item.run === 'published'">已发布 {{ item.version || '' }}{{ item.tag ? ' · tag ' + item.tag : '' }}</template>
          <template v-else-if="item.run === 'blocked'">卡在 {{ ((item.steps || [])[item.step] || {}).n || '' }}：{{ item.blocked || '' }}</template>
          <template v-else-if="item.run === 'waiting'">全链已跑通，就差你一句「发不发」</template>
          <template v-else-if="item.run === 'running'">正在跑，可以随时介入</template>
        </span>
        <span class="spacer" style="flex: 1" />
        <span class="small muted">{{ item.pbc }} · 作者 {{ item.by }}</span>
      </div>
    </div>

    <div v-if="item.kind !== 'Story'" class="card">
      <div class="ch"><span v-html="icon('doc')" /><h3>这类项不跑代码链路</h3></div>
      <div class="small muted">它推进的是制品或配置，不是软件交付。</div>
    </div>

    <div class="card">
      <div class="ch"><span v-html="icon('git')" /><h3>执行链路</h3><span class="spacer" /><span class="small muted">七步，只有 publish 是人工闸</span></div>
      <div class="tl">
        <div v-for="(st, i) in item.steps || []" :key="i" class="tl-item" :class="stepCls(st)">
          <div class="tl-dot">{{ stepMark(st) }}</div>
          <div class="tl-h">
            <b>{{ st.n }}</b>
            <span v-if="st.human" class="tag t-warn" style="font-size: 9px">人工闸</span>
            <span v-if="st.st === 'done' && st.at" class="small muted">{{ st.at }}</span>
            <span class="spacer" style="flex: 1" />
          </div>
          <div class="tl-d">{{ st.d }}</div>
        </div>
      </div>
    </div>

    <div v-if="item.run === 'idea' || item.run === 'blocked'" class="row" style="margin-bottom: 11px; gap: 6px; flex-wrap: wrap">
      <button class="btn primary" :disabled="!canBuild || busy" @click="run">
        <span v-html="icon('play', 12)" /> {{ item.run === 'blocked' ? '重新发起' : '发起直通' }}
      </button>
      <span class="small muted">
        {{ canBuild ? '会自动跑完制品→提交→测试→镜像→' + item.env + '，停在发布前由 SA 拍板' : '当前角色没有 build 权限（DA 专属）' }}
      </span>
    </div>

    <div v-if="item.run === 'waiting'" class="gate">
      <span v-html="icon('alert')" />
      <b>就差这一步：是否发布生产？</b>
      <span class="small muted">其余全部跑通（制品/提交/测试/镜像/{{ item.env }}）。</span>
      <input v-model="note" placeholder="备注（可选）" style="width: 180px; padding: 4px 8px; font-size: 11px" />
      <span class="spacer" style="flex: 1" />
      <button class="btn small" :disabled="!canTag || busy" @click="decide(false)">退回修改</button>
      <button class="btn primary small" :disabled="!canTag || busy" @click="decide(true)"><span v-html="icon('check', 12)" /> 发布生产</button>
    </div>
    <div v-if="!canTag && item.run === 'waiting'" class="perm-note">
      <span v-html="icon('lock', 12)" /> 发布拍板是 <b>SA</b> 的权限；当前角色点下去会被 403 拒绝，界面不会假装成功。
    </div>

    <div v-if="item.run === 'published'" class="ws clean" style="margin-top: 4px">
      <div class="ws-h"><span v-html="icon('check')" /> 已发布生产 · {{ item.version || '' }}<span v-if="item.url" style="color: var(--info)"> · {{ item.url }}</span></div>
    </div>

    <div class="card">
      <div class="ch"><span v-html="icon('layers')" /><h3>这条工作项的账本事实</h3><span class="spacer" /><span class="small muted">{{ facts.length }} 条</span></div>
      <div v-if="!facts.length" class="small muted">账本里还没有与它相关的事实。</div>
      <div v-for="f in facts" :key="f.seq" class="row" style="padding: 5px 0; border-bottom: 1px solid var(--line-e); font-size: 11.5px">
        <span class="mono small" style="color: var(--tx-11); width: 42px">#{{ f.seq }}</span>
        <span class="tag t-gray" style="font-size: 9px">{{ f.type }}</span>
        <span class="muted" style="flex: 1">{{ factText(f) }}</span>
        <span class="small muted">{{ f.actor }} · {{ f.at }}</span>
      </div>
    </div>
  </template>
</template>
