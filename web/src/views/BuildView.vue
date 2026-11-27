<script setup>
import { computed, ref, watch } from 'vue';
import PageHeader from '../components/PageHeader.vue';
import AgentStudio from '../components/AgentStudio.vue';
import QualityReport from '../components/QualityReport.vue';
import MaterialRuntime from '../components/MaterialRuntime.vue';
import { S, unitKind, currentAgent, currentUnit, toast, refresh, pushRuntime } from '../store.js';
import { api } from '../api.js';
import { icon } from '../icons.js';
import { latest, kindCls, kindText, ART, WORKER_NAME } from '../derive.js';
import { md2html } from '../md.js';

const versions = computed(() => (S.state && S.state.versions) || {});
const kind = computed(() => unitKind());
const material = computed(() => currentUnit()?.profile === 'material-inbound');
const hasSvc = computed(() => kind.value !== 'agent');
const isAgent = computed(() => kind.value !== 'pbc');
const agent = computed(() => currentAgent());
const code = computed(() => latest(versions.value, 'code') || {});
const gates = computed(() => (S.state && S.state.gates) || { all: [] });
const svc = computed(() => (S.state && S.state.svcVerify) || { pass: 0, total: 0, fails: [] });
const test = computed(() => (S.state && S.state.test) || null);
const builds = computed(() => (S.state && S.state.builds) || {});
const head = computed(() => {
  const cs = (S.state && S.state.commits) || [];
  return cs.length ? cs[0].no : '';
});

// 标签页按构成给：纯 PBC 有源码与预览，Agent 有编排，组合两者都有，
// 质量报告对三种都有（机器自检统一收拢在这里）。
const tab = computed(() => {
  const t = S.bpTab;
  if (t === 'backlog') return 'backlog';
  if (t === 'agentdef' && isAgent.value) return 'agentdef';
  if (t === 'code' && hasSvc.value) return 'code';
  if (t === 'quality') return 'quality';
  if (t === 'preview' && hasSvc.value) return 'preview';
  return isAgent.value ? 'agentdef' : 'code';
});

const runningBadge = { text: '执行中', tone: 't-warn' };
const workflowBadges = {
  running: runningBadge,
  waiting: { text: '待发布', tone: 't-warn' },
  verified: { text: '待打 tag', tone: 't-ok' },
  published: { text: '已发布', tone: 't-ok' },
  paused: { text: '已介入', tone: 't-warn' },
  blocked: { text: '受阻', tone: 't-err' },
  failed: { text: '失败', tone: 't-err' },
};
const taskBadges = {
  todo: { text: '待领', tone: 't-gray' },
  doing: runningBadge,
  done: { text: '已完成', tone: 't-ok' },
};

function backlogBadge(item) {
  // 历史 Task 的 verified 只是任务完成状态，打 tag 属于交付 Story。
  if (item.kind !== 'Story' && item.run === 'verified') return taskBadges.done;
  return workflowBadges[item.run] || taskBadges[item.st] || taskBadges.todo;
}

const items = computed(() => S.inbox.filter((x) => x.pbc === S.pbcId)
  .map((item) => ({ ...item, badge: backlogBadge(item) })));

// v3.2：Backlog 页的顺序是「工作项 → 文档」。
// 打开 Backlog 的人想看的是「这版要干哪些活」，不是先读一遍需求文档；
// 文档是给它做注脚的，默认收起，点开才渲染。
const bkVers = computed(() => (versions.value && versions.value.backlog) || []);
const bkV = ref('');
const showDoc = ref(false);
watch(
  () => [S.pbcId, bkVers.value.length],
  () => {
    const arr = bkVers.value;
    bkV.value = arr.length ? arr[arr.length - 1].v : '';
    showDoc.value = false;
  },
  { immediate: true },
);
const bkLatest = computed(() => (bkVers.value.length ? bkVers.value[bkVers.value.length - 1].v : ''));
const bkCur = computed(() => bkVers.value.find((v) => v.v === bkV.value) || bkVers.value[bkVers.value.length - 1] || null);
const isHist = computed(() => !!bkV.value && bkV.value !== bkLatest.value);
const bkDoc = computed(() => (bkCur.value && (bkCur.value.md || bkCur.value.note)) || '（Backlog 文档随提交进树）');
const bkLines = computed(() => (bkCur.value && bkCur.value.md ? String(bkCur.value.md).split('\n').length : 0));

// story 与 task 分层：story 行加深加粗，task 用 └ 挂在下面；
// 没挂 story 上的人负责任务（补 BRD、拆 PBC、登记接口）单独一段。
const stories = computed(() => items.value.filter((x) => x.kind === 'Story'));
const tasksOf = (sid) => items.value.filter((x) => x.kind !== 'Story' && x.parent === sid);
const looseTasks = computed(() => items.value.filter((x) => x.kind !== 'Story' && !x.parent));
const doingCount = computed(() => items.value.filter((x) => x.badge === runningBadge).length);

// 代码编辑器：草稿内容从投影里取；改动只有「保存为草稿」才会落账。
const files = ref({});
const path = ref('');
const dirty = ref(false);
watch(
  () => [S.pbcId, code.value.v],
  () => {
    files.value = JSON.parse(JSON.stringify(code.value.files || {}));
    const keys = Object.keys(files.value);
    path.value = keys.length ? keys[0] : '';
    dirty.value = false;
  },
  { immediate: true },
);

const fileList = computed(() => Object.keys(files.value));
const current = computed(() => files.value[path.value] || '');

function onCodeInput(e) {
  files.value[path.value] = e.target.value;
  dirty.value = true;
}

async function saveCode() {
  if (!dirty.value) {
    toast('代码没有改动', 'warn');
    return;
  }
  try {
    const j = await api.saveDraft(S.pbcId, { artifact: 'code', files: files.value, note: '人工修改：' + path.value });
    pushRuntime('STATE', 'artifact.drafted code ' + j.version);
    toast('代码已变更 → ' + j.version + '，此前自测/镜像/部署作废', 'ok');
  } catch (e) {
    toast(e.message, 'err');
  }
  await refresh();
}

function ownerText(o) {
  return WORKER_NAME[o] || o || '待派';
}
</script>

<template>
  <PageHeader
    :title="'构建 <span class=&quot;mono&quot; style=&quot;color:var(--ok);font-size:13px&quot;>代码 ' + (code.v || '—') + '</span>'"
    :sub="kind === 'agent'
      ? '给 Agent 开发员工用的编排页：画流程、配节点、调提示词、试跑一条；产物是一份编排 + digest，机器评测在「质量报告」。'
      : kind === 'hybrid'
        ? '两条线各建各的：服务走代码与镜像，Agent 走编排与 digest。Agent 那部分在「Agent 编排」tab 里画。'
        : 'Backlog → 代码 → 镜像。机器自检（四闸 / 还原 / 测试）在这一页的「质量报告」里；人上手验收在「验证」阶段。'"
  >
    <button class="btn small" @click="S.view = 'verify'"><span v-html="icon('check', 12)" /> 去验证</button>
  </PageHeader>

  <div class="ptabs">
    <div class="ptab" :class="{ on: tab === 'backlog' }" @click="S.bpTab = 'backlog'">
      <span v-html="icon('list', 11)" /> Backlog
      <span class="tag t-gray" style="font-size: 9px; margin-left: 7px">{{ items.length }}</span>
    </div>
    <div v-if="hasSvc" class="ptab" :class="{ on: tab === 'code' }" @click="S.bpTab = 'code'">
      <span v-html="icon('cpu', 11)" /> 源码与流水线
    </div>
    <div v-if="isAgent" class="ptab" :class="{ on: tab === 'agentdef' }" @click="S.bpTab = 'agentdef'">
      <span v-html="icon('robot', 11)" /> Agent 编排
    </div>
    <div class="ptab" :class="{ on: tab === 'quality' }" @click="S.bpTab = 'quality'">
      <span v-html="icon('check', 11)" /> 质量报告
    </div>
    <div v-if="hasSvc" class="ptab" :class="{ on: tab === 'preview' }" @click="S.bpTab = 'preview'">
      <span v-html="icon('cloud', 11)" /> 启动预览
    </div>
    <div class="spacer" />
    <span class="small muted">机器自检在这 · 人验收在「验证」</span>
  </div>

  <!-- Backlog（v3.2）：版本下拉 → 工作项 → 文档收在最后 -->
  <template v-if="tab === 'backlog'">
    <div class="ws clean">
      <div class="ws-h">
        <span v-html="icon('list')" /> Backlog
        <select v-model="bkV" class="bug-sev" style="font-size: 10.5px; margin-left: 7px; min-width: 92px">
          <option v-for="v in bkVers" :key="v.v" :value="v.v">{{ v.v }}{{ v.v === bkLatest ? '（当前）' : '' }}</option>
        </select>
        <span v-if="isHist" class="tag t-gray" style="font-size: 9px">历史版本</span>
        <span class="spacer" style="flex: 1" />
        <span class="small muted">{{ bkCur ? bkCur.by + ' · ' + bkCur.at : '还没有 Backlog 版本' }}</span>
      </div>
      <div class="small muted">文档里是 story 拆分口径、点数与待确认项；要改就去展开，要干活就看下面那张表。</div>
    </div>

    <div v-if="isHist" class="card">
      <div class="empty">历史版本只保文档，不保执行投影——工作项会被后续提交改写。</div>
    </div>

    <div v-else class="card">
      <div class="ch">
        <span v-html="icon('list')" /><h3>工作项</h3>
        <span class="spacer" />
        <span class="small muted">{{ stories.length }} story · {{ items.length - stories.length }} task · 执行中 {{ doingCount }} 条</span>
      </div>
      <table class="tb backlog-tb">
        <thead><tr><th style="width: 62px">编号</th><th>工作项</th><th style="width: 46px">点</th><th style="width: 110px">负责</th><th style="width: 66px">状态</th><th style="width: 56px" /></tr></thead>
        <tbody>
          <tr v-if="!items.length"><td colspan="6" class="small muted" style="padding: 14px">这个单元还没有工作项，去项目页需求池提一条。</td></tr>
          <template v-for="s in stories" :key="s.id">
            <tr class="bk-story">
              <td class="mono small">{{ s.id }}</td>
              <td class="small bk-title">{{ s.t }}</td>
              <td class="small muted">{{ s.pt || '' }}</td>
              <td class="small">{{ ownerText(s.owner) }}</td>
              <td><span class="tag" :class="s.badge.tone" style="font-size: 9px">{{ s.badge.text }}</span></td>
              <td><button class="btn small" @click="S.pbcId = s.pbc; S.itemId = s.id; S.view = 'item'">查看</button></td>
            </tr>
            <tr v-for="x in tasksOf(s.id)" :key="x.id" class="bk-task">
              <td class="mono small">{{ x.id }}</td>
              <td class="small bk-title"><span class="bk-branch">└</span>{{ x.t }}</td>
              <td class="small muted">{{ x.pt || '' }}</td>
              <td class="small">{{ ownerText(x.owner) }}</td>
              <td><span class="tag" :class="x.badge.tone" style="font-size: 9px">{{ x.badge.text }}</span></td>
              <td><button class="btn small" @click="S.pbcId = x.pbc; S.itemId = x.id; S.view = 'item'">查看</button></td>
            </tr>
          </template>
          <tr v-if="looseTasks.length" class="bk-orphan">
            <td colspan="6" class="small">人负责的任务 · 不挂在 story 下</td>
          </tr>
          <tr v-for="x in looseTasks" :key="x.id">
            <td class="mono small">{{ x.id }}</td>
            <td class="small bk-title"><span class="bk-branch">└</span>{{ x.t }}</td>
            <td class="small muted">{{ x.pt || '' }}</td>
            <td class="small">{{ ownerText(x.owner) }}</td>
            <td><span class="tag" :class="x.badge.tone" style="font-size: 9px">{{ x.badge.text }}</span></td>
            <td><button class="btn small" @click="S.pbcId = x.pbc; S.itemId = x.id; S.view = 'item'">查看</button></td>
          </tr>
        </tbody>
      </table>
    </div>

    <div class="card">
      <div class="ch">
        <span v-html="icon('doc')" /><h3>Backlog 文档</h3>
        <span class="spacer" />
        <span class="mono small" style="color: var(--info)">{{ bkV || '—' }}.md</span>
        <span class="small muted">{{ bkLines }} 行 · 随提交进树</span>
        <button class="btn small" style="margin-left: 8px" @click="showDoc = !showDoc">{{ showDoc ? '收起' : '展开全文' }}</button>
      </div>
      <div v-if="showDoc" class="pv" style="max-height: 380px; overflow: auto; border: 1px solid var(--line-1); border-radius: 8px; padding: 12px 18px" v-html="md2html(bkDoc)" />
      <div v-else class="small muted">文档默认收起：展开看 story 拆分口径、点数与待确认项；工作项是它的执行投影。</div>
    </div>
  </template>

  <!-- Agent 编排工作台 -->
  <template v-else-if="tab === 'agentdef'">
    <AgentStudio :agent="agent" />
  </template>

  <!-- 质量报告（机器自检） -->
  <template v-else-if="tab === 'quality'">
    <div class="small muted" style="margin-bottom: 8px">
      机器自检 · {{ kind === 'pbc' ? (material ? '材料入库业务测试 · 代码 ' : '通用示例测试 · 代码 ') + (code.v || '—') : kind === 'hybrid' ? '服务 ' + svc.pass + '/' + svc.total + ' · Agent ' + (agent ? agent.eval[0] : 0) + '/' + (agent ? agent.eval[1] : 0) : (agent ? agent.eval[0] : 0) + '/' + (agent ? agent.eval[1] : 0) }} · 判定归验证阶段的人
    </div>
    <QualityReport :kind="kind" :gates="gates" :agent="agent" :head="head" :svc="svc" :test="test" :builds="builds" />
  </template>

  <!-- 启动预览 -->
  <template v-else-if="tab === 'preview'">
    <MaterialRuntime v-if="material" />
    <div v-else class="card empty">通用示例未配置可操作的业务预览。</div>
  </template>

  <!-- 源码与流水线 -->
  <template v-else>
    <div class="ws" :class="{ clean: !dirty }">
      <div class="ws-h"><span v-html="icon(dirty ? 'alert' : 'check')" /> {{ dirty ? '有未保存的代码改动' : '源码与账本一致' }}</div>
      <div class="small muted">保存为草稿才算一次版本变化；未保存的内容不会进入构建。</div>
    </div>
    <div v-if="!fileList.length" class="card"><div class="empty">代码制品还没有文件内容。Agent 生成或人工保存草稿后会显示在这里。</div></div>
    <div v-else class="bwrap">
      <div class="ftree">
        <div class="ftree-h"><span v-html="icon('box', 11)" /> 文件</div>
        <div v-for="p in fileList" :key="p" class="fitem" :class="{ on: p === path }" @click="path = p">
          <span v-html="icon('doc', 11)" /><span class="fname">{{ p.split('/').pop() }}</span>
        </div>
        <div class="ftree-f"><span class="mono">{{ code.v }}</span><span class="small muted">{{ code.source || '生成' }}</span></div>
      </div>
      <div class="codepane">
        <div class="ed-bar">
          <span class="mono small" style="color: var(--info)">{{ path }}</span>
          <span class="spacer" style="flex: 1" />
          <span class="small muted">{{ current.split('\n').length }} 行</span>
          <span v-if="dirty" class="tag t-warn" style="margin-left: 8px">未保存</span>
          <button class="btn small primary" style="margin-left: 8px" @click="saveCode">保存为草稿</button>
        </div>
        <textarea spellcheck="false" :value="current" @input="onCodeInput" />
        <div class="row" style="padding: 5px 10px; border-top: 1px solid var(--line-1); background: var(--bg-panel); font-size: 10.5px; color: var(--tx-11)">
          <span class="mono">CODE {{ code.v }}</span>
          <span>· 衍生自 {{ (code.df || []).map((d) => d.t.toUpperCase() + ' ' + d.v).join(' · ') || '—' }}</span>
          <span class="spacer" style="flex: 1" /><span>改代码 = 新版本，自测/镜像/部署作废</span>
        </div>
      </div>
      <div class="pipe-col">
        <div class="step done"><div class="step-n">① 代码生成</div><div class="step-s">X-Dev · 依据 AT + Backlog</div></div>
        <div class="step" :class="code.st === 'draft' ? 'doing' : ''"><div class="step-n">② 提交</div><div class="step-s">版本变化原子落地</div></div>
        <div class="step"><div class="step-n">③ 自测</div><div class="step-s">单测 + BDD 验收，在质量报告</div></div>
        <div class="step"><div class="step-n">④ 构建镜像</div><div class="step-s">产出 digest 写回提交</div></div>
        <div class="step"><div class="step-n">⑤ 部署交付</div><div class="step-s">部署到环境</div></div>
        <div class="small muted" style="margin-top: 8px; line-height: 1.7">
          流水线的推进由 Dapr workflow 完成（DA 在需求池点「发起」），这里只展示当前投影——页面不自己跑流水线。
        </div>
      </div>
    </div>
  </template>
</template>
