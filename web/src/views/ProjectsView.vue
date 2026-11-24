<script setup>
import { computed } from 'vue';
import PageHeader from '../components/PageHeader.vue';
import { S, refresh } from '../store.js';
import { api } from '../api.js';
import { icon } from '../icons.js';
import { kindCls, kindText, WORKER_NAME } from '../derive.js';
import { toast } from '../store.js';

const canBacklog = computed(() => (S.acts || []).includes('backlog'));
const title = '';
// 需求池 = 全部交付单元的工作项（跨单元同表），和 /api/inbox 同源。
const rows = computed(() => S.inbox.slice().sort((a, b) => String(a.pbc).localeCompare(String(b.pbc)) || String(a.id).localeCompare(String(b.id))));
const busy = computed(() => S.agents.filter((a) => (a.st || []).includes('doing')).length);

function stTag(st) {
  return st === 'done' ? 't-ok' : st === 'doing' ? 't-info' : 't-gray';
}
function stText(st) {
  return st === 'done' ? '已完成' : st === 'doing' ? '进行中' : '待领取';
}
function runText(run) {
  return { idea: '未发起', draft: '未发起', running: '执行中', waiting: '待发布', paused: '已介入', blocked: '受阻', published: '已发布' }[run] || run || '—';
}
function runCls(run) {
  return 'cr-state ' + (['published', 'running', 'waiting', 'paused', 'blocked'].includes(run) ? run : 'draft');
}
function openItem(x) {
  S.pbcId = x.pbc;
  S.itemId = x.id;
  S.view = 'item';
}
function ownerName(o) {
  return WORKER_NAME[o] || o || '—';
}

const newTitle = computed({
  get: () => S.newTask || '',
  set: (v) => (S.newTask = v),
});

async function submit() {
  const t = (S.newTask || '').trim();
  if (!t) {
    toast('先写一句要做什么', 'err');
    return;
  }
  try {
    const j = await api.createTask(S.pbcId, { title: t, type: 'feature', env: 'test' });
    S.newTask = '';
    toast('已创建 ' + j.id + '；发起交付请切到 DA 点「发起」', 'ok');
  } catch (e) {
    toast(e.message, 'err');
  }
  await refresh();
}
</script>

<template>
  <PageHeader
    title="项目 · 悦府物业 · 收费与工单数字化"
    sub="项目是承接容器：一个项目一份 BRD，BRD 拆成多个交付单元（PBC / Agent / 组合），每个单元各自交付。"
  >
    <button class="btn small" @click="S.view = 'brd'"><span v-html="icon('doc', 12)" /> 打开 BRD</button>
  </PageHeader>

  <div class="card">
    <div class="ch">
      <span v-html="icon('doc')" /><h3>BRD（项目级制品）</h3><span class="spacer" />
      <span class="tag" :class="S.brd && S.brd.v ? 't-ok' : 't-gray'">{{ (S.brd && S.brd.v) || '—' }}</span>
    </div>
    <div class="small muted">
      由 BA 渐进编写，X-业务分析员可续写；每次保存生成一个草稿版本，走与制品相同的版本线。
      当前 {{ S.brd ? (S.brd.versions || []).length : 0 }} 个版本 · {{ (S.brd && S.brd.words) || 0 }} 字。
    </div>
    <div class="row" style="margin-top: 9px"><button class="btn small" @click="S.view = 'brd'">编辑 / 查看版本线</button></div>
  </div>

  <div class="card">
    <div class="ch">
      <span v-html="icon('layers')" /><h3>交付单元</h3><span class="spacer" />
      <span class="small muted">同一套五阶段，三种构成</span>
    </div>
    <div
      v-for="g in [['pbc', '微服务 PBC'], ['agent', 'Agent'], ['hybrid', 'PBC + Agent']]"
      :key="g[0]"
    >
      <div v-if="S.units.some((u) => u.kind === g[0])" class="row" style="padding: 8px 0; border-bottom: 1px solid var(--line-e); align-items: flex-start; gap: 9px">
        <span class="tag" :class="kindCls(g[0])" style="font-size: 9px; min-width: 92px; justify-content: center">{{ g[1] }}</span>
        <div class="row wrap" style="gap: 6px; flex: 1">
          <button
            v-for="u in S.units.filter((x) => x.kind === g[0])"
            :key="u.id"
            class="btn small"
            @click="S.pbcId = u.id; S.view = 'plan'"
          >
            <span v-html="icon(g[0] === 'agent' ? 'robot' : 'layers', 12)" /> {{ u.name }}
            <span class="mono small" style="color: var(--accent-tx2)">{{ u.head }}</span>
          </button>
        </div>
      </div>
    </div>
    <div class="arch-note" style="margin-top: 8px">
      PBC 交付微服务，验证看还原与验收；Agent 交付数字员工，验证看评测集；{{ kindText('hybrid') }} 两者一起发，两条判据都要过。
    </div>
  </div>

  <div class="card">
    <div class="ch">
      <span v-html="icon('list')" /><h3>需求池 · 工作项</h3><span class="spacer" />
      <span class="small muted">{{ rows.length }} 项 · 数字员工在推进 {{ busy }} 项</span>
      <button class="btn small" style="margin-left: 8px" :disabled="!canBacklog" @click="submit">+ 提交并直通</button>
    </div>
    <div class="small muted" style="margin-bottom: 8px">
      点任意一行看它的执行链路。提交即直通：自动跑完制品→提交→测试→构建→部署 test，只停在「发不发生产」。
    </div>
    <div v-if="canBacklog" class="cr-form" style="margin-bottom: 10px">
      <input id="cr-title" v-model="newTitle" placeholder="要做什么（一句话）" @keyup.enter="submit" />
      <button class="btn primary small" @click="submit">提交并直通</button>
    </div>
    <table class="tb">
      <thead>
        <tr>
          <th style="width: 62px">编号</th><th>内容</th><th style="width: 66px">单元</th>
          <th style="width: 52px">点数</th><th style="width: 88px">执行</th><th style="width: 128px">交付状态</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="x in rows" :key="x.pbc + '/' + x.id" style="cursor: pointer" @click="openItem(x)">
          <td class="mono" :style="{ color: x.kind === 'Story' ? 'var(--purple)' : 'var(--tx-6)' }">{{ x.id }}</td>
          <td>{{ x.t }}<div class="small muted">{{ x.kind }} · {{ ownerName(x.owner) }}</div></td>
          <td class="small muted">{{ x.pbc }}</td>
          <td class="mono">{{ x.pt }}</td>
          <td><span class="tag" :class="stTag(x.st)">{{ stText(x.st) }}</span></td>
          <td><span :class="runCls(x.run)">{{ runText(x.run) }}</span></td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
