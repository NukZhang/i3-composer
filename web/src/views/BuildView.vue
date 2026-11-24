<script setup>
import { computed, ref, watch } from 'vue';
import PageHeader from '../components/PageHeader.vue';
import AgentStudio from '../components/AgentStudio.vue';
import QualityReport from '../components/QualityReport.vue';
import { S, unitKind, currentAgent, toast, refresh, pushRuntime } from '../store.js';
import { api } from '../api.js';
import { icon } from '../icons.js';
import { latest, kindCls, kindText, ART, WORKER_NAME } from '../derive.js';

const versions = computed(() => (S.state && S.state.versions) || {});
const kind = computed(() => unitKind());
const hasSvc = computed(() => kind.value !== 'agent');
const isAgent = computed(() => kind.value !== 'pbc');
const agent = computed(() => currentAgent());
const code = computed(() => latest(versions.value, 'code') || {});
const gates = computed(() => (S.state && S.state.gates) || { all: [] });
const svc = computed(() => (S.state && S.state.svcVerify) || { pass: 0, total: 0, fails: [] });
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

const items = computed(() => S.inbox.filter((x) => x.pbc === S.pbcId));

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

  <!-- Backlog -->
  <template v-if="tab === 'backlog'">
    <div class="ws clean">
      <div class="ws-h"><span v-html="icon('list')" /> Backlog {{ (latest(versions, 'backlog') || {}).v }} 是构建阶段的输入<span class="spacer" style="flex: 1" /><span class="small muted">{{ (latest(versions, 'backlog') || {}).by || '—' }} · {{ (latest(versions, 'backlog') || {}).at || '—' }}</span></div>
      <div class="small muted">方案阶段只产出 PRD / AD / AT。到这一步才把验收标准拆成可执行任务，拆出来的每条都进需求池，由 worker 领活。</div>
    </div>
    <div class="card">
      <div class="ch"><span v-html="icon('git')" /><h3>本单元的工作项</h3><span class="spacer" /><span class="small muted">{{ items.length }} 条 · 执行中 {{ items.filter((x) => x.st === 'doing').length }}</span></div>
      <table class="tb">
        <thead><tr><th style="width: 62px">编号</th><th>工作项</th><th style="width: 46px">点</th><th style="width: 86px">负责</th><th style="width: 66px">状态</th><th style="width: 56px" /></tr></thead>
        <tbody>
          <tr v-if="!items.length"><td colspan="6" class="small muted" style="padding: 14px">这个单元还没有工作项，去项目页需求池提一条。</td></tr>
          <tr v-for="x in items" :key="x.id">
            <td class="mono small">{{ x.id }}</td>
            <td class="small">{{ x.t }}</td>
            <td class="small muted">{{ x.pt || '' }}</td>
            <td class="small">{{ ownerText(x.owner) }}</td>
            <td><span class="tag" :class="x.st === 'done' ? 't-ok' : x.st === 'doing' ? 't-warn' : 't-gray'" style="font-size: 9px">{{ x.st === 'done' ? '已完成' : x.st === 'doing' ? '执行中' : '待领' }}</span></td>
            <td><button class="btn small" @click="S.pbcId = x.pbc; S.itemId = x.id; S.view = 'item'">查看</button></td>
          </tr>
        </tbody>
      </table>
    </div>
    <div class="card">
      <div class="ch"><span v-html="icon('doc')" /><h3>Backlog 文档</h3><span class="spacer" /><span class="mono small" style="color: var(--info)">{{ (latest(versions, 'backlog') || {}).v }}.md</span></div>
      <div class="pv" style="max-height: 380px; overflow: auto; border: 1px solid var(--line-1); border-radius: 8px; padding: 12px 18px">{{ (latest(versions, 'backlog') || {}).note || '（Backlog 文档随提交进树）' }}</div>
      <div class="small muted" style="margin-top: 7px">这份 md 随提交进树（tree.backlog），是交付物的一部分；工作项是它的执行投影。</div>
    </div>
  </template>

  <!-- Agent 编排工作台 -->
  <template v-else-if="tab === 'agentdef'">
    <AgentStudio :agent="agent" />
  </template>

  <!-- 质量报告（机器自检） -->
  <template v-else-if="tab === 'quality'">
    <div class="small muted" style="margin-bottom: 8px">
      机器自检 · {{ kind === 'pbc' ? '代码 ' + (code.v || '—') : kind === 'hybrid' ? '服务 ' + svc.pass + '/' + svc.total + ' · Agent ' + (agent ? agent.eval[0] : 0) + '/' + (agent ? agent.eval[1] : 0) : (agent ? agent.eval[0] : 0) + '/' + (agent ? agent.eval[1] : 0) }} · 判定归验证阶段的人
    </div>
    <QualityReport :kind="kind" :gates="gates" :agent="agent" :head="head" :svc="svc" />
  </template>

  <!-- 启动预览 -->
  <template v-else-if="tab === 'preview'">
    <div class="urlbar">
      <span class="dotok" /><span class="u">http://localhost:18080/bills</span>
      <span class="spacer" style="flex: 1" />
      <span class="small">服务已启动 · 健康检查 200 · 代码 {{ code.v || '—' }}</span>
      <button class="btn small" @click="toast('已重启服务（示例）', 'ok')">重启</button>
    </div>
    <div class="ifr-lg" style="border-radius: 0 0 9px 9px; border-top: 0">
      <div class="empty" style="padding: 60px 0; color: var(--tx-11)">
        预览页需要真跑起来的服务；本仓库只到「构建产物 + digest」这一层。
      </div>
    </div>
    <div class="small muted" style="margin: 8px 0 11px">这是代码跑起来之后的真实页面，不是线框图。接 CI 时这里换成真实地址。</div>
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
