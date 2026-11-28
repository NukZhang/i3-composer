<script setup>
import { computed, ref, watch } from 'vue';
import PageHeader from '../components/PageHeader.vue';
import AgentStudio from '../components/AgentStudio.vue';
import QualityReport from '../components/QualityReport.vue';
import { S, unitKind, currentAgent, toast, refresh, pushRuntime } from '../store.js';
import { api } from '../api.js';
import { icon } from '../icons.js';
import { latest, kindCls, kindText, ART, WORKER_NAME } from '../derive.js';
import { md2html } from '../md.js';

const versions = computed(() => (S.state && S.state.versions) || {});
const kind = computed(() => unitKind());
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

const items = computed(() => S.inbox.filter((x) => x.pbc === S.pbcId));

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
const doingCount = computed(() => items.value.filter((x) => x.st === 'doing').length);

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

// ---- 生成说明 / AC ↔ 测试 / 本次 vs 上一版（#96） ----
//
// 这一页能审的只有「生成器真的产出了什么」。所以三件事写清楚：
//   1. 当前生成器只产出 Go 单文件骨架，术语按工作项标题选（#93）；
//      规格 / 方案 / 任务三层视图要等 #90 产出真数据，这里不摆假占位。
//   2. 代码里的每一条 AC 落在哪个测试用例——对照的是真文件内容，不是清单文案。
//   3. 本次生成与上一版逐行差了什么——用的是账本里已有的版本历史，不新增事实。
const codeVers = computed(() => (versions.value && versions.value.code) || []);
const prevVer = computed(() => {
  const arr = codeVers.value;
  return arr.length >= 2 ? arr[arr.length - 2] : null;
});
const curFiles = computed(() => code.value.files || {});
const prevFiles = computed(() => (prevVer.value && prevVer.value.files) || {});
const diffPaths = computed(() => {
  const out = [];
  const seen = new Set();
  for (const p of [...Object.keys(prevFiles.value), ...Object.keys(curFiles.value)]) {
    if (seen.has(p)) continue;
    seen.add(p);
    const a = prevFiles.value[p];
    const b = curFiles.value[p];
    if (a !== b) out.push(p);
  }
  return out;
});
const diffOpen = ref(false);
// 逐行 LCS：文件不大（骨架三份），慢不了；只为显示，不影响任何判定。
function lineDiff(a, b) {
  const A = String(a == null ? '' : a).split('\n');
  const B = String(b == null ? '' : b).split('\n');
  const n = A.length;
  const m = B.length;
  const L = Array.from({ length: n + 1 }, () => new Int32Array(m + 1));
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      L[i][j] = A[i] === B[j] ? L[i + 1][j + 1] + 1 : Math.max(L[i + 1][j], L[i][j + 1]);
    }
  }
  const out = [];
  let i = 0;
  let j = 0;
  while (i < n && j < m) {
    if (A[i] === B[j]) {
      out.push({ t: ' ', s: A[i] });
      i++;
      j++;
    } else if (L[i + 1][j] >= L[i][j + 1]) {
      out.push({ t: '-', s: A[i] });
      i++;
    } else {
      out.push({ t: '+', s: B[j] });
      j++;
    }
  }
  while (i < n) out.push({ t: '-', s: A[i++] });
  while (j < m) out.push({ t: '+', s: B[j++] });
  return out;
}
const diffs = computed(() =>
  diffPaths.value.map((p) => ({
    path: p,
    lines: lineDiff(prevFiles.value[p], curFiles.value[p]),
    added: lineDiff(prevFiles.value[p], curFiles.value[p]).filter((l) => l.t === '+').length,
    removed: lineDiff(prevFiles.value[p], curFiles.value[p]).filter((l) => l.t === '-').length,
  })),
);

// AC 清单的取法有讲究：必须取「这一版代码自己依存的 AT」。
//
// 不能直接拿 S.state.checks 去对测试注释：种子单元的 checks 是旧清单，
// 它的 AC-1 是「账期按自然月生成」，而代码里的 AC-1 是「健康检查可用」——
// id 撞了、语义不同，硬对会把两条不相干的东西配成一对，看着像验过了。
// 所以先按 code.df 里记的 at 版本取那一版 AT 的 AC（这才是代码的依据），
// 取不到才退回验收清单，再取不到就只列代码里真的断言了的 AC。
const acList = computed(() => {
  const arr = (versions.value && versions.value.at) || [];
  const df = (code.value.df || []).find((d) => d.t === 'at');
  const at = (df && arr.find((v) => v.v === df.v)) || arr[arr.length - 1] || null;
  if (at && at.md) {
    const rows = [...String(at.md).matchAll(/^###\s*(AC-\d+)\s+(.+)$/gm)].map((m) => ({ id: m[1], n: m[2].trim() }));
    if (rows.length) return { src: 'at', v: at.v, rows, stale: !!(df && at.v !== df.v) };
  }
  const list = (S.state && S.state.checks) || [];
  if (list.length) return { src: 'checks', v: '', rows: list.map((c) => ({ id: c.id, n: c.n })), stale: false };
  return { src: 'code', v: '', rows: [], stale: false };
});

// AC 在代码里的落点：测试文件里按「// TestXxx：AC-n」的注释对齐。
// 没有对应用例就如实显示「没找到」——那说明这条 AC 没被验。
const acMap = computed(() => {
  const tests = curFiles.value['main_test.go'] || '';
  const decls = [...tests.matchAll(/\/\/\s*(\w+)：\s*(AC-\d+)/g)].map((m) => ({ fn: m[1], ac: m[2] }));
  const rows = acList.value.rows.length
    ? acList.value.rows
    : [...new Set(decls.map((d) => d.ac))].sort().map((id) => ({ id, n: '' }));
  return rows.map((a) => ({ id: a.id, n: a.n, fn: (decls.find((d) => d.ac === a.id) || {}).fn || '' }));
});
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
              <td><span class="tag" :class="s.st === 'done' ? 't-ok' : s.st === 'doing' ? 't-warn' : 't-gray'" style="font-size: 9px">{{ s.st === 'done' ? '已完成' : s.st === 'doing' ? '执行中' : '待领' }}</span></td>
              <td><button class="btn small" @click="S.pbcId = s.pbc; S.itemId = s.id; S.view = 'item'">查看</button></td>
            </tr>
            <tr v-for="x in tasksOf(s.id)" :key="x.id" class="bk-task">
              <td class="mono small">{{ x.id }}</td>
              <td class="small bk-title"><span class="bk-branch">└</span>{{ x.t }}</td>
              <td class="small muted">{{ x.pt || '' }}</td>
              <td class="small">{{ ownerText(x.owner) }}</td>
              <td><span class="tag" :class="x.st === 'done' ? 't-ok' : x.st === 'doing' ? 't-warn' : 't-gray'" style="font-size: 9px">{{ x.st === 'done' ? '已完成' : x.st === 'doing' ? '执行中' : '待领' }}</span></td>
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
            <td><span class="tag" :class="x.st === 'done' ? 't-ok' : x.st === 'doing' ? 't-warn' : 't-gray'" style="font-size: 9px">{{ x.st === 'done' ? '已完成' : x.st === 'doing' ? '执行中' : '待领' }}</span></td>
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
      机器自检 · {{ kind === 'pbc' ? '代码 ' + (code.v || '—') : kind === 'hybrid' ? '服务 ' + svc.pass + '/' + svc.total + ' · Agent ' + (agent ? agent.eval[0] : 0) + '/' + (agent ? agent.eval[1] : 0) : (agent ? agent.eval[0] : 0) + '/' + (agent ? agent.eval[1] : 0) }} · 判定归验证阶段的人
    </div>
    <QualityReport :kind="kind" :gates="gates" :agent="agent" :head="head" :svc="svc" :test="test" :builds="builds" />
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

    <!-- 生成说明（#96）：先把「这是什么生成的、边界在哪」摆出来，
         再让人看文件。不这样的话，第一眼就是三个 Go 文件，人会以为
         这是按自己的工作项定制的实现。 -->
    <div class="card gennote">
      <div class="ch">
        <span v-html="icon('cpu', 11)" /><h3>这一版代码是怎么来的</h3>
        <span class="spacer" />
        <span class="tag t-warn" style="font-size: 9px">生成器能力有限</span>
      </div>
      <div class="small" style="line-height: 1.75">
        当前生成器产出的是 <b>Go 单文件骨架</b>：记录结构、进程内存储、健康检查，以及列表 / 统计 / 生成三个接口；
        资源名与字段按工作项标题选（例如标题里的「缴费单」→ <code>invoices</code>、「工单」→ <code>tickets</code>），
        认不出来就用通用记录。它<b>不是</b>按本单元的规格实现的代码——
        「规格 / 方案 / 任务」三层视图要等 spec 环节接上（#90）才有真数据，这里不摆假占位。
      </div>
      <div class="row wrap" style="gap: 5px; margin-top: 8px">
        <span class="tag t-gray" style="font-size: 9px">语言：只有 Go（#91）</span>
        <span class="tag t-gray" style="font-size: 9px">模板：单一骨架，无增量（#92 / #94）</span>
        <span class="tag t-gray" style="font-size: 9px">口径：不写死业务费率与种子数据（#93）</span>
        <span class="tag t-gray" style="font-size: 9px">质量闸：未覆盖「按规格实现」（#95）</span>
      </div>
    </div>

    <!-- AC ↔ 测试：每条验收落在哪个用例。对不上就如实说，不假装验过。 -->
    <div v-if="acMap.length" class="card">
      <div class="ch">
        <span v-html="icon('check', 11)" /><h3>验收落点</h3>
        <span class="spacer" />
        <span class="small muted">{{ acMap.filter((a) => a.fn).length }}/{{ acMap.length }} 条有对应用例</span>
      </div>
      <div class="acmap">
        <div v-for="a in acMap" :key="a.id" class="acmap-row">
          <span class="mono small" :class="a.fn ? 'ok' : 'miss'">{{ a.id }}</span>
          <span class="small" style="flex: 1">{{ a.n || '—' }}</span>
          <button v-if="a.fn" class="btn small" @click="path = 'main_test.go'">
            <span class="mono" style="font-size: 10px">{{ a.fn }}</span>
          </button>
          <span v-else class="tag t-warn" style="font-size: 9px">代码里没有对应用例</span>
        </div>
      </div>
      <div class="small muted" style="margin-top: 7px">
        清单取自这一版代码依存的 <code>AT {{ acList.v || '—' }}</code>，落点按测试文件里的
        <code>// TestXxx：AC-n</code> 注释对齐——验的是文件内容，不是验收页的文案。
        <template v-if="acList.src === 'checks'">（没找到那一版 AT，退回验收清单，编号可能对不上，慎读。）</template>
        <template v-if="acList.src === 'code'">（没有清单，只列代码里真的断言了的 AC。）</template>
      </div>
    </div>

    <!-- 本次 vs 上一版：用账本里已有的版本历史算，不新增事实。 -->
    <div v-if="prevVer" class="card">
      <div class="ch">
        <span v-html="icon('layers', 11)" /><h3>本次生成改了什么</h3>
        <span class="spacer" />
        <span class="small muted">{{ code.v }} vs {{ prevVer.v }} · {{ diffPaths.length }} 个文件有变化</span>
        <button class="btn small" style="margin-left: 8px" @click="diffOpen = !diffOpen">
          {{ diffOpen ? '收起 diff' : '看 diff' }}
        </button>
      </div>
      <div v-if="!diffPaths.length" class="small muted">这一版与上一版逐字节相同（重新生成不会改内容：生成是确定性的）。</div>
      <template v-else>
        <div class="row wrap" style="gap: 5px">
          <span v-for="d in diffs" :key="d.path" class="tag t-gray mono" style="font-size: 9px">
            {{ d.path }} <span style="color: var(--ok)">+{{ d.added }}</span> <span style="color: var(--err)">-{{ d.removed }}</span>
          </span>
        </div>
        <div v-if="diffOpen" class="diffbox">
          <template v-for="d in diffs" :key="'b' + d.path">
            <div class="diff-h mono">{{ d.path }}</div>
            <div v-for="(l, i) in d.lines" :key="i" class="diff-l" :class="l.t === '+' ? 'add' : l.t === '-' ? 'del' : ''">
              <span class="sign">{{ l.t }}</span><span class="code">{{ l.s || ' ' }}</span>
            </div>
          </template>
        </div>
      </template>
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

<style scoped>
/* #96：生成说明 / AC 落点 / diff 三块的样式。跟页面既有卡片同口径，
   只用已有的色变量，不引入新主题。 */
.gennote .small code { font-family: var(--mono, ui-monospace); font-size: 10.5px; padding: 0 3px; background: var(--bg-panel); border-radius: 3px; }
.acmap { display: flex; flex-direction: column; gap: 4px; }
.acmap-row { display: flex; align-items: center; gap: 8px; padding: 4px 0; border-bottom: 1px solid var(--line-e); }
.acmap-row:last-child { border-bottom: 0; }
.acmap-row .ok { color: var(--ok); }
.acmap-row .miss { color: var(--warn); }
.diffbox { margin-top: 8px; border: 1px solid var(--line-e); border-radius: 6px; overflow: auto; max-height: 320px; background: var(--bg-panel); }
.diff-h { position: sticky; top: 0; padding: 4px 8px; background: var(--bg-panel); border-bottom: 1px solid var(--line-e); font-size: 10px; color: var(--tx-11); }
.diff-l { display: flex; gap: 8px; padding: 1px 8px; font-family: var(--mono, ui-monospace); font-size: 10.5px; white-space: pre; }
.diff-l .sign { width: 8px; color: var(--tx-11); }
.diff-l.add { background: color-mix(in srgb, var(--ok) 12%, transparent); }
.diff-l.add .sign { color: var(--ok); }
.diff-l.del { background: color-mix(in srgb, var(--err) 10%, transparent); }
.diff-l.del .sign { color: var(--err); }
</style>
