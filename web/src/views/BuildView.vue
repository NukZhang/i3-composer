<script setup>
import { computed, ref, watch } from 'vue';
import PageHeader from '../components/PageHeader.vue';
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

// 标签页：backlog 永远有；源码页只有 PBC/组合；Agent 定义页只有 Agent/组合。
const tab = computed(() => {
  const t = S.bpTab;
  if (t === 'backlog') return 'backlog';
  if (t === 'agentdef' && isAgent.value) return 'agentdef';
  if (t === 'code' && hasSvc.value) return 'code';
  if (t === 'source' && hasSvc.value) return 'source';
  return isAgent.value && !hasSvc.value ? 'agentdef' : hasSvc.value ? 'code' : 'backlog';
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

const TOOL_DESC = {
  'repo.read': '读代码与提交历史', 'repo.write': '改代码（进工作区，不直接提交）', 'shell.exec': '执行命令',
  'test.run': '跑单测与验收', 'git.commit': '提交（本 Agent 无此权限）',
  'proto.screenshot': '截运行态页面', 'diff.compare': '与原型逐项比对', 'report.write': '写测试报告',
  'proto.read': '读原型基线', 'proto.write': '生成原型页面', 'style.token': '取设计 token', 'asset.pick': '选素材',
  'image.build': '构建镜像', 'k8s.apply': '部署到环境', 'health.check': '健康检查', 'rollback': '回滚',
  'doc.read': '读文档与访谈', 'doc.write': '写文档草稿', 'brd.outline': '补 BRD 大纲', 'backlog.split': '拆 backlog',
  'customer.read': '读客户档案与分层', 'bill.read': '读账单与逾期', 'strategy.read': '读催缴策略', 'msg.draft': '生成话术草稿（不发送）',
};
function toolKind(t) {
  if (/(read|get|query|list|check|compare|screenshot|outline|run)/.test(t)) return '只读';
  if (/(send|apply|deploy|publish|commit|rollback|exec)/.test(t)) return '有副作用';
  if (/(write|draft|build|split|pick)/.test(t)) return '写';
  return '内部';
}
function approvalCls(t) {
  const k = toolKind(t);
  return k === '只读' ? 't-ok' : k === '有副作用' ? 't-err' : 't-warn';
}
function approvalText(t) {
  const k = toolKind(t);
  return k === '只读' ? '不需要' : k === '有副作用' ? '人工点确认' : '进工作区待确认';
}
function ownerText(o) {
  return WORKER_NAME[o] || o || '待派';
}
</script>

<template>
  <PageHeader
    :title="'构建 <span class=&quot;mono&quot; style=&quot;color:var(--ok);font-size:13px&quot;>代码 ' + (code.v || '—') + '</span>'"
    :sub="kind === 'agent'
      ? 'Backlog → Agent 定义 → digest。产物是一份声明 + 一个 actor 类型，不是进程。'
      : kind === 'hybrid'
        ? 'Backlog → 服务代码 + Agent 定义 → 镜像 & digest。两条线各有自己的构建产物。'
        : 'Backlog → 代码 → 镜像。这一步只负责「做出来并跑起来」；像不像原型、验收过不过，在验证阶段判。'"
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
      <span v-html="icon('robot', 11)" /> Agent 定义
    </div>
    <div class="spacer" />
    <span class="small muted">还原对比与测试在「验证」阶段</span>
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

  <!-- Agent 定义 -->
  <template v-else-if="tab === 'agentdef'">
    <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 9px">
      <div class="card" style="margin: 0">
        <div class="ch"><span v-html="icon('git')" /><h3>本版改了什么</h3><span class="spacer" /><span class="mono small" style="color: var(--info)">{{ agent && agent.ver }}</span></div>
        <div class="tree-box" style="margin: 0">
          <div class="tree-line"><span class="k">评测</span><span class="v">{{ agent ? agent.eval[0] : 0 }}/{{ agent ? agent.eval[1] : 0 }}</span></div>
          <div class="tree-line"><span class="k">成本</span><span class="v">{{ (agent && agent.cost) || '—' }}</span></div>
          <div class="tree-line"><span class="k">digest</span><span class="v">{{ (agent && agent.digest) || '—' }}</span></div>
        </div>
        <div class="small muted" style="margin-top: 7px">改提示词、改工具、改超时都会换 digest。没换 digest 就不是新版本。</div>
      </div>
      <div class="card" style="margin: 0">
        <div class="ch"><span v-html="icon('box')" /><h3>产物组成</h3><span class="spacer" /><span class="small muted">一份声明 + 一个 actor 类型</span></div>
        <div class="tree-box" style="margin: 0">
          <div class="tree-line"><span class="k">prompt</span><span class="v">{{ ((agent && agent.prompt) || '').length }} 字 · 系统提示词</span></div>
          <div class="tree-line"><span class="k">tools</span><span class="v">{{ ((agent && agent.tools) || []).length }} 个 · 白名单，越权即拒</span></div>
          <div class="tree-line"><span class="k">evalset</span><span class="v">{{ agent ? agent.eval[1] : 0 }} 条 · 版本锁定</span></div>
          <div class="tree-line"><span class="k">model</span><span class="v">{{ (agent && agent.model) || '—' }}</span></div>
          <div class="tree-line"><span class="k">harness</span><span class="v">dsh · session 续跑 · 超时 900s</span></div>
        </div>
      </div>
    </div>

    <div class="card">
      <div class="ch"><span v-html="icon('lock')" /><h3>工具与权限</h3><span class="spacer" /><span class="small muted">白名单之外的工具一律拒绝，不做「灵活处理」</span></div>
      <table class="tb">
        <thead><tr><th style="width: 150px">工具</th><th style="width: 74px">类型</th><th style="width: 110px">审批</th><th>说明</th></tr></thead>
        <tbody>
          <tr v-for="t in (agent && agent.tools) || []" :key="t">
            <td class="mono small" style="color: var(--info)">{{ t }}</td>
            <td class="small muted">{{ toolKind(t) }}</td>
            <td><span class="tag" :class="approvalCls(t)" style="font-size: 9px">{{ approvalText(t) }}</span></td>
            <td class="small muted">{{ TOOL_DESC[t] || '—' }}</td>
          </tr>
        </tbody>
      </table>
    </div>

    <div class="card">
      <div class="ch"><span v-html="icon('cpu')" /><h3>Dapr Agent 定义</h3><span class="spacer" /><span class="mono small">agents/{{ (agent && agent.id) || '—' }}.yaml</span></div>
      <div class="tree-box" style="font-size: 11px">
        <div class="tree-line"><span class="k">apiVersion</span><span class="v">dapr.io/v1alpha1</span></div>
        <div class="tree-line"><span class="k">kind</span><span class="v">Agent</span></div>
        <div class="tree-line"><span class="k">actor</span><span class="v">agent-{{ (agent && agent.id) || '—' }}　（一个 Agent 一个 actor，同时只跑一轮）</span></div>
        <div class="tree-line"><span class="k">model</span><span class="v">{{ (agent && agent.model) || '—' }}</span></div>
        <div class="tree-line"><span class="k">harness</span><span class="v">dsh · session 续跑，超时 900s</span></div>
        <div class="tree-line"><span class="k">tools</span><span class="v">{{ ((agent && agent.tools) || []).join(' · ') }}</span></div>
        <div class="tree-line"><span class="k">workflow</span><span class="v">agent-lifecycle（方案→原型→构建→验证→发布）</span></div>
        <div class="tree-line"><span class="k">state</span><span class="v">statestore · key=session/{actorId}</span></div>
        <div class="tree-line"><span class="k">digest</span><span class="v">{{ (agent && agent.digest) || '—' }}</span></div>
      </div>
    </div>
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
        <div class="step"><div class="step-n">③ 自测</div><div class="step-s">单测 + BDD 验收，在验证阶段</div></div>
        <div class="step"><div class="step-n">④ 构建镜像</div><div class="step-s">产出 digest 写回提交</div></div>
        <div class="step"><div class="step-n">⑤ 部署交付</div><div class="step-s">部署到环境</div></div>
        <div class="small muted" style="margin-top: 8px; line-height: 1.7">
          流水线的推进由 Dapr workflow 完成（DA 在需求池点「发起」），这里只展示当前投影——页面不自己跑流水线。
        </div>
      </div>
    </div>
  </template>
</template>
