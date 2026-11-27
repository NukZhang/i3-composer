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
const hasWorkflow = computed(() => item.value?.kind === 'Story' && !!item.value.steps?.length);

// 这一轮真跑出来的结论：测试条数、构建摘要、部署地址。
const testResult = computed(() => (S.state && S.state.test) || null);
const buildDigest = computed(() => {
  const st = S.state || {};
  const head = (st.commits || [])[0];
  return head ? (st.builds || {})[head.no] || '' : '';
});
const deployURL = computed(() => {
  const ds = (S.state && S.state.deploys) || [];
  return ds.length ? ds[ds.length - 1].url : '';
});

const note = ref('');
const busy = ref(false);

// 入口准入的结论挂在工作项上（admit 字段，来自 item.admitted / item.admit.rejected）。
const admit = computed(() => (item.value && item.value.admit) || null);
// 被拒的原因：一人一行。它是后端 err 原文的一部分，这里只负责把三行摊开显示，
// 不改写措辞——「缺什么 / 找谁 / 补哪」是后端给的，前端不发明。
const rejectLines = computed(() => (admit.value && admit.value.reasons) || []);
const waivers = computed(() => (admit.value && admit.value.waivers) || []);

// 补输入的表单：只补三项硬项（验收口径 / 数据范围 / 责任人）。
const fixing = ref(false);
const fix = ref({ accept: '', data: '', acceptBy: '', complyBy: '' });

async function run() {
  busy.value = true;
  try {
    const j = await api.runItem(item.value.id, item.value.pbc);
    toast(item.value.id + ' 已发起（实例 ' + j.instance + '）', 'ok');
    fixing.value = false;
  } catch (e) {
    toast(e.message, 'err');
    // 被准入挡下时，直接把「补哪」的表单摊出来，不让用户猜怎么补。
    if (e.status === 400 && /准入/.test(e.message)) fixing.value = true;
  }
  busy.value = false;
  await refresh();
}

async function submitFix() {
  const body = {
    accept: fix.value.accept.split('\n').map((s) => s.trim()).filter(Boolean),
    data: fix.value.data.split(/[\n,，、]/).map((s) => s.trim()).filter(Boolean),
    acceptBy: fix.value.acceptBy.trim(),
    complyBy: fix.value.complyBy.trim(),
  };
  if (!body.accept.length && !body.data.length && !body.acceptBy && !body.complyBy) {
    toast('至少补一项再提交', 'err');
    return;
  }
  busy.value = true;
  try {
    await api.admitInputs(item.value.id, body, item.value.pbc);
    toast('准入输入已补进账本，可以再发起', 'ok');
    fixing.value = false;
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
  if (run === 'verified' && item.value.kind !== 'Story') return '已完成';
  return { idea: '未发起', draft: '未发起', running: '执行中', waiting: '待发布', verified: '待打 tag', paused: '已介入', blocked: '受阻', failed: '失败', published: '已发布' }[run] || run || '—';
}
function runCls(run) {
  return 'cr-state ' + (['published', 'running', 'waiting', 'verified', 'paused', 'blocked', 'failed'].includes(run) ? run : 'draft');
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
    'item.admitted': '入口准入放行',
    'item.admit.rejected': '入口准入拒绝',
    'item.admit.inputs': '补准入输入',
    'item.run.started': '发起交付（workflow 已启动）',
    'item.step.done': '一步完成',
    'item.waiting': '到发布闸，等待拍板',
    'publish.decided': '人的发布决定',
    'item.published': '已发布（打 tag）',
    'item.blocked': '受阻',
    'tag.created': '打了 tag',
    'test.run': '真跑测试',
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

// factDetail 从事实 payload 里取出结论。账本不只记「发生过什么」，
// 还要能就地核对「到底发生了什么」——测试几条过、digest 是哪个、部署在哪。
function factDetail(f) {
  const p = f.payload || {};
  switch (f.type) {
    case 'test.run':
      return `${p.pass}/${p.total} 通过` + (p.failures && p.failures.length ? `（${p.failures.join('、')}）` : '');
    case 'build.succeeded':
      return p.digest ? String(p.digest).slice(0, 19) + '…' : '';
    case 'deploy.succeeded':
      return p.url || '';
    case 'item.published':
      return `${p.tag || ''} · v${p.version || ''}`;
    case 'publish.decided':
      return p.approve ? '同意发布' : '不同意发布';
    case 'item.waiting':
      return p.reason || '等 SA 拍板';
    default:
      return '';
  }
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
          <template v-else-if="hasWorkflow && item.run === 'waiting'">全链已跑通，就差你一句「发不发」</template>
          <template v-else-if="hasWorkflow && item.run === 'running'">正在跑，可以随时介入</template>
        </span>
        <span class="spacer" style="flex: 1" />
        <span class="small muted">{{ item.pbc }} · 作者 {{ item.by }}</span>
      </div>
    </div>

    <!-- 入口准入：放行了但软项没补 → 黄条（别把自己卡死，也别假装五项全判过） -->
    <div v-if="admit && admit.st === 'admitted' && waivers.length" class="ws warn" style="margin-bottom: 11px">
      <div class="ws-h">
        <span v-html="icon('alert')" /> 准入放行，但这两项还没补齐（waiver）
      </div>
      <ul class="admit-list">
        <li v-for="(w, i) in waivers" :key="i">{{ w }}</li>
      </ul>
      <div class="small muted">放行不等于判过：这些是软项，不挡发起，但会一直挂在账本上，直到补上。</div>
    </div>

    <!-- 入口准入：硬项不过被拒 → 三行「缺什么 / 找谁 / 补哪」，并就地补 -->
    <div v-if="admit && admit.st === 'rejected'" class="gate" style="flex-direction: column; align-items: stretch; gap: 8px">
      <div class="row" style="gap: 8px; align-items: center">
        <span v-html="icon('lock')" />
        <b>入口准入没过 · {{ admit.at }} 由 {{ admit.by }} 判</b>
        <span class="spacer" style="flex: 1" />
        <button v-if="canBacklog" class="btn small" @click="fixing = !fixing">{{ fixing ? '收起补录' : '补输入' }}</button>
      </div>
      <ul class="admit-list">
        <li v-for="(r, i) in rejectLines" :key="i">{{ r }}</li>
      </ul>
      <div v-if="!canBacklog" class="small muted">补准入输入是 BA 的业务动作；当前角色只能看，补不了。</div>
      <div v-if="fixing && canBacklog" class="admit-fix">
        <div class="small muted">按「补哪」的顺序逐项补。补的是输入，发起时会重新判。</div>
        <label>验收口径（每行一条，写成「当……则……」）
          <textarea v-model="fix.accept" rows="2" placeholder="当业主提交缴费单，则应生成幂等键且重复提交不重复入账" /></label>
        <label>数据范围（库表 / 字段，逗号或换行分隔）
          <input v-model="fix.data" placeholder="fee.bill.amount, fee.bill.due_date" /></label>
        <div class="row" style="gap: 8px">
          <label style="flex: 1">验收人<input v-model="fix.acceptBy" placeholder="BA 业务" /></label>
          <label style="flex: 1">合规背责人<input v-model="fix.complyBy" placeholder="SA 系统" /></label>
        </div>
        <div class="row" style="gap: 6px">
          <button class="btn primary small" :disabled="busy" @click="submitFix">补进账本</button>
        </div>
      </div>
    </div>

    <div v-if="!hasWorkflow" class="card">
      <div class="ch"><span v-html="icon('doc')" /><h3>这类项不跑代码链路</h3></div>
      <div class="small muted">它推进的是制品或配置，不是软件交付。</div>
    </div>

    <div v-else class="card">
      <div class="ch">
        <span v-html="icon('git')" /><h3>执行链路</h3><span class="spacer" />
        <span class="small muted">{{ item.steps.length }} 步</span>
      </div>
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

    <div v-if="hasWorkflow && ['idea', 'blocked', 'failed'].includes(item.run)" class="row" style="margin-bottom: 11px; gap: 6px; flex-wrap: wrap">
      <button class="btn primary" :disabled="!canBuild || busy" @click="run">
        <span v-html="icon('play', 12)" /> {{ item.run === 'idea' ? '发起直通' : '重新发起' }}
      </button>
      <span class="small muted">
        {{ canBuild ? '会自动跑完制品→提交→测试→镜像→' + item.env + '，停在发布前由 SA 拍板' : '当前角色没有 build 权限（DA 专属）' }}
      </span>
    </div>

    <div v-if="hasWorkflow && item.run === 'waiting'" class="gate">
      <span v-html="icon('alert')" />
      <b>就差这一步：是否发布生产？</b>
      <span class="small muted">其余全部跑通（制品/提交/测试/镜像/{{ item.env }}）。</span>
      <input v-model="note" placeholder="备注（可选）" style="width: 180px; padding: 4px 8px; font-size: 11px" />
      <span class="spacer" style="flex: 1" />
      <button class="btn small" :disabled="!canTag || busy" @click="decide(false)">退回修改</button>
      <button class="btn primary small" :disabled="!canTag || busy" @click="decide(true)"><span v-html="icon('check', 12)" /> 发布生产</button>
    </div>
    <div v-if="hasWorkflow && !canTag && item.run === 'waiting'" class="perm-note">
      <span v-html="icon('lock', 12)" /> 发布拍板是 <b>SA</b> 的权限；当前角色点下去会被 403 拒绝，界面不会假装成功。
    </div>

    <div v-if="item.run === 'published'" class="ws clean" style="margin-top: 4px">
      <div class="ws-h"><span v-html="icon('check')" /> 已发布生产 · {{ item.version || '' }}<span v-if="item.url" style="color: var(--info)"> · {{ item.url }}</span></div>
    </div>

    <div v-if="testResult || buildDigest || deployURL" class="card">
      <div class="ch"><span v-html="icon('check')" /><h3>这一轮真跑出来的结论</h3><span class="spacer" />
        <span class="small muted">测试条数来自 go test，摘要是编译产物的 sha256</span>
      </div>
      <div class="row wrap" style="gap: 6px">
        <span v-if="testResult" class="achip" :style="'border-color:' + (testResult.pass === testResult.total ? 'var(--line-ok)' : 'var(--line-err)')">
          <b :style="{ color: testResult.pass === testResult.total ? 'var(--ok)' : 'var(--err)' }">{{ testResult.pass }}/{{ testResult.total }}</b>
          测试通过
        </span>
        <span v-if="buildDigest" class="achip"><b>{{ buildDigest.slice(0, 19) }}…</b> 构建产物摘要</span>
        <span v-if="deployURL" class="achip"><b style="color: var(--info)">{{ deployURL }}</b> 部署地址</span>
      </div>
      <div v-if="testResult && testResult.failures && testResult.failures.length" class="small" style="color: var(--err); margin-top: 7px">
        失败用例：{{ testResult.failures.join('、') }}
      </div>
    </div>

    <div class="card">
      <div class="ch"><span v-html="icon('layers')" /><h3>这条工作项的账本事实</h3><span class="spacer" /><span class="small muted">{{ facts.length }} 条</span></div>
      <div v-if="!facts.length" class="small muted">账本里还没有与它相关的事实。</div>
      <div v-for="f in facts" :key="f.seq" class="row" style="padding: 5px 0; border-bottom: 1px solid var(--line-e); font-size: 11.5px">
        <span class="mono small" style="color: var(--tx-11); width: 42px">#{{ f.seq }}</span>
        <span class="tag t-gray" style="font-size: 9px">{{ f.type }}</span>
        <span class="muted" style="flex: 1">{{ factText(f) }}<span v-if="factDetail(f)" class="mono small" style="color: var(--tx-11); margin-left: 6px">{{ factDetail(f) }}</span></span>
        <span class="small muted">{{ f.actor }} · {{ f.at }}</span>
        <span v-if="f.actor === 'seed'" class="tag t-gray" style="font-size: 8px">演示数据</span>
      </div>
    </div>
  </template>
</template>
