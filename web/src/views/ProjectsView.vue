<script setup>
import { computed, ref } from 'vue';
import PageHeader from '../components/PageHeader.vue';
import { S, refresh, toast, currentProject, currentWorkspace, unitsOfProject } from '../store.js';
import { api } from '../api.js';
import { icon } from '../icons.js';
import { kindCls, kindText, WORKER_NAME } from '../derive.js';

// 项目页：一个项目一份 BRD、多个交付单元。空项目就该看起来是空的——
// 不塞假数据，否则评审时会把「模板」当成「已经做出来的东西」。
const proj = computed(() => currentProject());
const ws = computed(() => currentWorkspace());
const units = computed(() => unitsOfProject(S.projId));
const brd = computed(() => S.brd || null);
const hasBRD = computed(() => !!(proj.value && proj.value.brd));

// 需求池只列本项目单元的工作项。
const unitIDs = computed(() => new Set(units.value.map((u) => u.id)));
const rows = computed(() =>
  S.inbox
    .filter((x) => unitIDs.value.has(x.pbc))
    .slice()
    .sort((a, b) => String(a.pbc).localeCompare(String(b.pbc)) || String(a.id).localeCompare(String(b.id))),
);
const canBacklog = computed(() => (S.acts || []).includes('backlog'));
const canPbc = computed(() => (S.acts || []).includes('pbc'));
const canBrd = computed(() => (S.acts || []).includes('brd'));

const creating = ref(false);
const nf = ref({ id: '', name: '', kind: 'pbc', agentId: '' });

function stTag(st) {
  return st === 'done' ? 't-ok' : st === 'doing' ? 't-info' : 't-gray';
}
function stText(st) {
  return st === 'done' ? '已完成' : st === 'doing' ? '进行中' : '待领取';
}
function runText(run) {
  return {
    idea: '未发起', draft: '未发起', running: '执行中',
    verified: '待打 tag', waiting: '待打 tag', tagged: '已交付',
    paused: '已介入', blocked: '受阻', published: '已交付',
  }[run] || run || '—';
}
function runCls(run) {
  return 'cr-state ' + (['published', 'running', 'waiting', 'paused', 'blocked'].includes(run) ? run : 'draft');
}
function openItem(x) {
  S.pbcId = x.pbc;
  S.itemId = x.id;
  S.view = 'item';
}
function openUnit(id) {
  S.pbcId = id;
  S.view = 'plan';
}
function ownerName(o) {
  return WORKER_NAME[o] || o || '—';
}

async function newBRD() {
  if (!proj.value) return;
  try {
    await api.startBRD(proj.value.id);
    toast('已建 0.1 草稿：边写边存，保存即新版本', 'ok');
    S.view = 'brd';
  } catch (e) {
    toast(e.message, 'err');
  }
  await refresh();
}

function startCreate() {
  creating.value = true;
  nf.value = { id: '', name: '', kind: 'pbc', agentId: '' };
}
async function submitUnit() {
  const f = nf.value;
  if (!f.id.trim() || !f.name.trim()) {
    toast('单元编号与名称都要填', 'err');
    return;
  }
  try {
    await api.createUnit({ ...f, proj: S.projId });
    toast('已建交付单元：' + f.name, 'ok');
    creating.value = false;
    await refresh();
  } catch (e) {
    toast(e.message, 'err');
  }
}

const newTitle = computed({
  get: () => S.newTask || '',
  set: (v) => (S.newTask = v),
});
// 入口准入要的三项硬输入（验收口径 / 数据范围 / 责任人）。建单时带上就能直接发起；
// 不带也能建（需求池先记一笔），但发起时会被准入挡下并点名缺什么、找谁、补哪。
const admitForm = ref({ accept: '', data: '', acceptBy: '', complyBy: '' });
const splitLines = (s) => String(s || '').split('\n').map((x) => x.trim()).filter(Boolean);
const splitList = (s) => String(s || '').split(/[\n,，、]/).map((x) => x.trim()).filter(Boolean);
async function submitTask() {
  const t = (S.newTask || '').trim();
  if (!t) {
    toast('先写一句要做什么', 'err');
    return;
  }
  try {
    const j = await api.createTask(S.pbcId, {
      title: t, type: 'feature', env: 'test',
      accept: splitLines(admitForm.value.accept),
      data: splitList(admitForm.value.data),
      acceptBy: admitForm.value.acceptBy.trim(),
      complyBy: admitForm.value.complyBy.trim(),
    });
    S.newTask = '';
    admitForm.value = { accept: '', data: '', acceptBy: '', complyBy: '' };
    toast('已创建 ' + j.id + '；发起交付请切到 DA 点「发起」', 'ok');
  } catch (e) {
    toast(e.message, 'err');
  }
  await refresh();
}
</script>

<template>
  <PageHeader
    :title="'项目 · ' + (proj ? ((ws ? ws.name + ' / ' : '') + proj.name) : '—')"
    :sub="'项目是承接容器：一个项目一份 BRD，BRD 拆成多个交付单元，每个单元各自交付。' + (ws ? '归属工作空间「' + ws.name + '」。' : '这个项目没归入工作空间，独立存在。')"
  >
    <button class="btn small" @click="S.view = 'brd'"><span v-html="icon('doc', 12)" /> 打开 BRD</button>
  </PageHeader>

  <div v-if="!proj" class="card">
    <div class="empty">还没有项目。点侧栏「项目」右侧的 ＋ 新建一个，或加入已有的。</div>
  </div>

  <template v-else>
    <!-- BRD：有就给版本，没有就给入口 -->
    <div v-if="hasBRD" class="card">
      <div class="ch">
        <span v-html="icon('doc')" /><h3>BRD（项目级制品）</h3><span class="spacer" />
        <span class="tag t-ok">{{ proj.brd.v }}</span>
      </div>
      <div class="small muted">
        {{ proj.brd.file }} · 负责人 {{ proj.owner || '—' }} · 由 BA 渐进编写，X-业务分析工程师可续写
      </div>
      <div class="row" style="margin-top: 9px">
        <button class="btn small" @click="S.view = 'brd'">编辑 / 查看版本线</button>
        <span v-if="proj.shared" class="small muted">共享项目 · 你看到的是对方的 BRD 版本</span>
        <span v-else-if="brd && brd.words" class="small muted">{{ (brd.versions || []).length }} 个版本 · {{ brd.words }} 字</span>
      </div>
    </div>
    <div v-else class="card" style="border-style: dashed">
      <div class="ch">
        <span v-html="icon('doc')" /><h3>还没有 BRD</h3><span class="spacer" />
        <span class="tag t-warn" style="font-size: 9px">待建</span>
      </div>
      <div class="small muted" style="line-height: 1.75">
        BRD 是这个项目唯一的入口：没有 BRD 就没有可拆的交付单元。<br />
        可以先写一版粗的，后面渐进补——它本来就不是一次写完的东西。
      </div>
      <div class="row" style="margin-top: 10px; gap: 6px">
        <button class="btn small primary" :disabled="!canBrd" @click="newBRD">
          <span v-html="icon('plus', 11)" /> 新建 BRD
        </button>
        <span class="small muted">新建后是 0.1 的空白模板（背景 / 目标 / 范围 / 关键规则 / 验收口径）</span>
      </div>
    </div>

    <!-- 交付单元 -->
    <div v-if="units.length" class="card">
      <div class="ch">
        <span v-html="icon('layers')" /><h3>交付单元</h3><span class="spacer" />
        <span class="small muted">同一套五阶段，三种构成</span>
        <button class="btn small" style="margin-left: 8px" :disabled="!canPbc" @click="startCreate">
          <span v-html="icon('plus', 11)" /> 新建单元
        </button>
      </div>
      <div v-for="g in [['pbc', '微服务 PBC'], ['agent', 'Agent']]" :key="g[0]">
        <div v-if="units.some((u) => (u.kind || 'pbc') === g[0])" class="row" style="padding: 8px 0; border-bottom: 1px solid var(--line-e); align-items: flex-start; gap: 9px">
          <span class="tag" :class="kindCls(g[0])" style="font-size: 9px; min-width: 92px; justify-content: center">{{ g[1] }}</span>
          <div class="row wrap" style="gap: 6px; flex: 1">
            <button v-for="u in units.filter((x) => (x.kind || 'pbc') === g[0])" :key="u.id" class="btn small" @click="openUnit(u.id)">
              <span v-html="icon(g[0] === 'agent' ? 'robot' : 'layers', 12)" /> {{ u.name }}
              <span class="mono small" style="color: var(--accent-tx2)">{{ u.head }}</span>
            </button>
          </div>
        </div>
      </div>
      <div class="arch-note" style="margin-top: 8px">
        只有两种：PBC 和 Agent。组合不是单元的属性——一个单元只有一个 tag；要「服务 + Agent 一起上」，在发布编排里把它们勾进同一批次。
      </div>
    </div>

    <div v-else class="card" style="border-style: dashed">
      <div class="ch">
        <span v-html="icon('layers')" /><h3>还没有交付单元</h3><span class="spacer" />
        <span class="tag t-gray" style="font-size: 9px">0 个</span>
      </div>
      <div class="small muted" style="line-height: 1.75">
        交付单元从 BRD 里拆。一个 BRD 可以拆成多个单元，每个单元独立走五阶段、独立打 tag，但它们共用这一份 BRD 和同一个需求池。
      </div>
      <div class="row" style="margin-top: 10px; gap: 6px">
        <!-- 拆解是 Agent 跑完后落草稿，没有可直接点的接口；以前这里是个亮着却按了没反应的按钮（#24）。
             改成说明文本，保留「手动新建」这条真正可用的路径。 -->
        <span class="small muted">有 BRD 后，拆解由 Agent 跑完落草稿</span>
        <button class="btn small" :disabled="!canPbc" @click="startCreate"><span v-html="icon('plus', 11)" /> 手动新建</button>
        <span v-if="!hasBRD" class="small muted">要先有 BRD</span>
      </div>
    </div>

    <!-- 手动新建交付单元 -->
    <div v-if="creating" class="card">
      <div class="ch"><span v-html="icon('plus')" /><h3>新建交付单元</h3><span class="spacer" /><span class="small muted">挂在项目「{{ proj.name }}」下</span></div>
      <div class="row wrap" style="gap: 8px; align-items: flex-end">
        <div class="field" style="margin: 0"><label>编号</label><input v-model="nf.id" placeholder="例如：gate" style="width: 140px" /></div>
        <div class="field" style="margin: 0"><label>名称</label><input v-model="nf.name" placeholder="例如：门禁通行" style="width: 200px" /></div>
        <div class="field" style="margin: 0">
          <label>构成</label>
          <select v-model="nf.kind" style="width: 130px">
            <option value="pbc">微服务 PBC</option>
            <option value="agent">Agent（交付的产物）</option>
          </select>
        </div>
        <div v-if="nf.kind !== 'pbc'" class="field" style="margin: 0">
          <label>Agent</label>
          <select v-model="nf.agentId" style="width: 160px">
            <option value="">选择一个交付的 Agent</option>
            <option v-for="a in S.agents.filter((x) => x.product)" :key="a.id" :value="a.id">{{ a.name }}</option>
          </select>
        </div>
        <button class="btn small primary" @click="submitUnit">创建</button>
        <button class="btn small" @click="creating = false">取消</button>
      </div>
    </div>

    <!-- 需求池 -->
    <div v-if="units.length" class="card">
      <div class="ch">
        <span v-html="icon('list')" /><h3>需求池 · 工作项</h3><span class="spacer" />
        <span class="small muted">{{ rows.length }} 项</span>
      </div>
      <div v-if="canBacklog" class="cr-form" style="margin-bottom: 10px">
        <input id="cr-title" v-model="newTitle" placeholder="要做什么（一句话）" @keyup.enter="submitTask" />
        <button class="btn primary small" @click="submitTask">提交并直通</button>
      </div>
      <!-- 建单时顺手把准入输入带上：不填也能建，但发起时准入会点名缺什么 -->
      <div v-if="canBacklog" class="admit-fix" style="margin-bottom: 10px; border-top: none; padding-top: 0">
        <div class="small muted">发起前准入要判的四项（可选，现在不填发起时会点名叫你补）</div>
        <label>验收口径（每行一条，写成「当……则……」）
          <textarea v-model="admitForm.accept" rows="2" placeholder="当业主提交缴费单，则应生成幂等键且重复提交不重复入账" /></label>
        <div class="row" style="gap: 8px">
          <label style="flex: 1">数据范围（逗号或换行分隔）
            <input v-model="admitForm.data" placeholder="fee.bill.amount, fee.bill.due_date" /></label>
        </div>
        <div class="row" style="gap: 8px">
          <label style="flex: 1">验收人<input v-model="admitForm.acceptBy" placeholder="BA 业务" /></label>
          <label style="flex: 1">合规背责人<input v-model="admitForm.complyBy" placeholder="SA 系统" /></label>
        </div>
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
    <div v-else class="card">
      <div class="ch"><span v-html="icon('list')" /><h3>需求池</h3><span class="spacer" /><span class="small muted">空</span></div>
      <div class="small muted" style="padding: 6px 0">需求池跟着交付单元走：有了单元，才谈得上 backlog。</div>
    </div>
  </template>
</template>
