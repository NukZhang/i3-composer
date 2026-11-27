<script setup>
import { computed, ref, watch } from 'vue';
import PageHeader from '../components/PageHeader.vue';
import { S, refresh, currentUnit, unitKind, currentAgent, toast } from '../store.js';
import { api } from '../api.js';
import { icon } from '../icons.js';
import { md2html } from '../md.js';
import { latest, findVer, drafts, staleList, kindCls, kindText, PLAN_ARTS, ART, nextCommitNo, nextTag } from '../derive.js';

const versions = computed(() => (S.state && S.state.versions) || {});
const commits = computed(() => (S.state && S.state.commits) || []);
const gates = computed(() => (S.state && S.state.gates) || { all: [] });
const unit = computed(() => currentUnit() || {});
const kind = computed(() => unitKind());
const agent = computed(() => currentAgent());
const ws = computed(() => drafts(versions.value));
const stale = computed(() => staleList(versions.value));
const head = computed(() => commits.value[0] || null);

const selCommit = computed(() => commits.value.find((c) => c.no === S.selCommit) || head.value);

// 编辑器状态：只存「未保存的文本」，保存后立刻回到投影。
const text = ref('');
const note = ref('');
const dirty = ref(false);

watch(
  () => [S.editArt, S.pbcId, latest(versions.value, S.editArt) && latest(versions.value, S.editArt).v],
  () => {
    const l = latest(versions.value, S.editArt);
    text.value = (l && l.md) || '';
    note.value = '';
    dirty.value = false;
  },
  { immediate: true },
);

function onInput(e) {
  text.value = e.target.value;
  dirty.value = true;
}

async function saveDraft() {
  if (!text.value.trim()) {
    toast('内容为空，不能保存', 'err');
    return;
  }
  try {
    const j = await api.saveDraft(S.pbcId, { artifact: S.editArt, text: text.value, note: note.value });
    toast('已保存为草稿 ' + j.version + '（进工作区）', 'ok');
  } catch (e) {
    toast(e.message, 'err');
  }
  await refresh();
}

function openArt(t) {
  S.editArt = t;
  S.planTab = 'edit';
}
function selC(no) {
  S.selCommit = no;
}
function typeMeta(t) {
  return {
    new: { n: '需求新增', cls: 't-info' },
    feature: { n: '需求变更', cls: 't-accent' },
    defect: { n: '缺陷修复', cls: 't-err' },
    backlog: { n: 'backlog 录入', cls: 't-gray' },
    doc: { n: '纯文档', cls: 't-gray' },
  }[t] || { n: t || '—', cls: 't-gray' };
}
function isHead(c) {
  return head.value && c.no === head.value.no;
}
function changesOf(c) {
  const i = commits.value.findIndex((x) => x.no === c.no);
  const older = commits.value[i + 1];
  return ['prd', 'proto', 'ad', 'at', 'backlog', 'code']
    .filter((t) => (c.tree && c.tree[t]) !== ((older && older.tree && older.tree[t]) || ''))
    .map((t) => ({ t, from: (older && older.tree && older.tree[t]) || '—', to: (c.tree && c.tree[t]) || '—' }));
}
const latestArt = computed(() => latest(versions.value, S.editArt) || {});

async function addAnnotationRound() {
  try {
    await api.addAnnotation({ x: 0.06, y: 0.18, w: 0.22, h: 0.16, text: '缴费单列表要支持按账期筛选', round: '待提交' });
    toast('批注已记录', 'ok');
  } catch (e) {
    toast(e.message, 'err');
  }
  await refresh();
}
</script>

<template>
  <PageHeader
    :title="unit.name + ' <span class=&quot;tag ' + kindCls(kind) + '&quot; style=&quot;vertical-align:3px&quot;>' + kindText(kind) + '</span>'"
    :sub="'HEAD <code style=&quot;color:var(--accent-tx2)&quot;>' + ((head && head.no) || '—') + '</code> · 已交付 ' + ((unit.tags || []).join(' / ') || '—') + ' · ' + (kind === 'agent' ? '交付物是 Agent 定义 + 评测集' : kind === 'hybrid' ? '交付物是服务代码 + Agent 定义' : '交付物是编辑好的 md')"
  >
    <button class="btn primary small" @click="S.view = 'projects'"><span v-html="icon('plus', 12)" /> 发起变更</button>
  </PageHeader>

  <div class="unitbar">
    <span class="tag" :class="kindCls(kind)" style="font-size: 9px">{{ kindText(kind) }}</span>
    <template v-if="kind === 'pbc'">
      <b>微服务 ×1</b><span class="muted">验证看还原对比 + 测试验收</span>
    </template>
    <template v-else-if="kind === 'agent'">
      <b>Agent ×1</b>
      <span class="mono">{{ (agent && agent.n) || unit.name }} v{{ (agent && agent.ver) || '—' }}</span>
      <span class="muted">模型 {{ (agent && agent.model) || '—' }} · 工具 {{ ((agent && agent.tools) || []).length }} 个</span>
      <span class="spacer" style="flex: 1" />
      <span class="muted">没有微服务，验证看评测集 {{ (agent && agent.eval && agent.eval[0]) || 0 }}/{{ (agent && agent.eval && agent.eval[1]) || 0 }}</span>
    </template>
    <template v-else>
      <b>微服务 ×1 + Agent ×1</b>
      <span class="mono">{{ (agent && agent.n) || unit.name }} v{{ (agent && agent.ver) || '—' }}</span>
      <span class="muted">服务管「什么时候催」，Agent 管「怎么说」</span>
      <span class="spacer" style="flex: 1" />
      <span class="muted">两条判据都要过</span>
    </template>
  </div>

  <div v-if="kind !== 'pbc' && agent" class="card">
    <div class="ch">
      <span v-html="icon('robot')" /><h3>方案 · Agent 定义（交付物本身，不是平台员工）</h3>
      <span class="spacer" />
      <span class="mono small" style="color: var(--info)">{{ agent.n }} v{{ agent.ver }}</span>
      <button class="btn small" style="margin-left: 8px" @click="S.agentId = agent.id; S.view = 'agent'"><span v-html="icon('robot', 11)" /> 打开</button>
    </div>
    <div class="small muted" style="margin-bottom: 5px">系统提示词</div>
    <div class="pv" style="border: 1px solid var(--line-1); border-radius: 8px; padding: 10px 13px; font-size: 11.5px; line-height: 1.8">{{ agent.prompt }}</div>
    <div class="small muted" style="margin: 10px 0 5px">工具白名单</div>
    <div class="row wrap" style="gap: 4px">
      <span v-for="t in agent.tools || []" :key="t" class="achip"><b>{{ t }}</b></span>
    </div>
  </div>

  <div class="ws" :class="{ clean: !ws.length && !stale.length }">
    <template v-if="ws.length || stale.length">
      <div class="ws-h">
        <span v-html="icon('alert')" /> 工作区：{{ ws.length }} 项未提交变更
        <span v-if="stale.length" style="color: var(--err)"> · {{ stale.length }} 个制品过期</span>
      </div>
      <div v-for="x in ws" :key="x.t" class="ws-item">
        <span v-html="icon('chev', 10)" />
        <b style="font-weight: 500">{{ ART[x.t].n }}</b>
        <span class="mono" style="color: var(--info)">{{ x.v.v }}</span>
        <span class="muted">{{ x.v.note || '' }}</span>
        <span class="tag t-purple" style="font-size: 9px">{{ x.v.by || '—' }}</span>
        <span class="muted" style="margin-left: auto">{{ x.v.at }}</span>
      </div>
      <div v-for="s in stale" :key="s.t" class="ws-item" style="color: var(--err)">
        <span v-html="icon('alert', 10)" />
        <b style="font-weight: 500">{{ ART[s.t].n }} {{ s.v.v }}</b>
        仍指向 {{ s.on.map((d) => d.t.toUpperCase() + ' ' + d.v).join('、') }}，与最新不同步
      </div>
      <div class="row" style="margin-top: 9px">
        <button class="btn primary small" :disabled="!!stale.length || !ws.length">提交为 {{ nextCommitNo(commits) }}</button>
        <span v-if="stale.length" class="small" style="color: var(--warn)">有 {{ stale.length }} 个制品过期，提交已锁定</span>
        <span v-else-if="!ws.length" class="small muted">工作区无变更</span>
        <span class="small muted">提交由 workflow 完成（DA 发起交付后自动跑）</span>
      </div>
    </template>
    <template v-else>
      <div class="ws-h"><span v-html="icon('check')" /> 工作区干净 · 与 HEAD {{ (head && head.no) || '—' }} 一致</div>
      <div class="small muted">编辑 PRD/AD/AT 后保存为草稿会进工作区；发起变更（需求/缺陷/backlog）由 Agent 落草稿。</div>
    </template>
  </div>

  <div class="ptabs">
    <div
      v-for="t in PLAN_ARTS"
      :key="t"
      class="ptab"
      :class="{ on: S.planTab === 'edit' && S.editArt === t }"
      @click="openArt(t)"
    >
      {{ ART[t].n }}
      <span v-if="ws.some((x) => x.t === t)" class="dot" />
    </div>
    <div class="ptab" :class="{ on: S.planTab === 'history' }" @click="S.planTab = 'history'">
      <span v-html="icon('git', 11)" /> 提交历史
    </div>
    <div class="spacer" />
    <template v-if="S.planTab === 'edit'">
      <span class="small muted" style="margin-right: 8px">
        {{ latestArt.v || '—' }} · {{ latestArt.st === 'draft' ? '草稿' : '已接受' }}
      </span>
    </template>
  </div>

  <template v-if="S.planTab === 'history'">
    <div style="display: grid; grid-template-columns: 286px 1fr; gap: 12px; align-items: start">
      <div class="card" style="margin: 0">
        <div class="ch"><span v-html="icon('git')" /><h3>提交历史</h3><span class="spacer" /><span class="small muted">{{ commits.length }} 次</span></div>
        <div class="clist">
          <div v-if="!commits.length" class="small muted">账本里还没有提交。</div>
          <div v-for="c in commits" :key="c.no" class="citem" :class="{ on: c.no === S.selCommit }" @click="selC(c.no)">
            <div class="top">
              <span class="no">{{ c.no }}</span>
              <span class="tag" :class="typeMeta(c.type).cls">{{ typeMeta(c.type).n }}</span>
              <span v-if="c.tag" class="tagname">{{ c.tag }}</span>
              <span v-if="isHead(c)" class="tag t-gray" style="margin-left: auto">HEAD</span>
            </div>
            <div class="msg">{{ c.msg }}</div>
            <div class="foot">{{ c.by }} · {{ c.at }}</div>
          </div>
        </div>
      </div>
      <div v-if="selCommit">
        <div class="card">
          <div class="ch">
            <span v-html="icon('git')" /><h3>提交 {{ selCommit.no }}</h3>
            <span class="tag" :class="typeMeta(selCommit.type).cls">{{ typeMeta(selCommit.type).n }}</span>
            <span v-if="selCommit.tag" class="tagname">{{ selCommit.tag }}</span>
            <span class="spacer" />
            <span class="tag" :class="isHead(selCommit) ? 't-info' : 't-gray'">{{ isHead(selCommit) ? 'HEAD' : '历史' }}</span>
          </div>
          <div class="small" style="color: var(--tx-4); margin-bottom: 8px">{{ selCommit.msg }}</div>
          <div class="small muted" style="margin-bottom: 9px">{{ selCommit.by }} · {{ selCommit.at }} · tree {{ selCommit.treeSha }}</div>
          <div class="small" style="color: var(--tx-7); margin-bottom: 5px">变更集</div>
          <div class="tree-box" style="margin-bottom: 10px">
            <div v-for="x in changesOf(selCommit)" :key="x.t" class="tree-line">
              <span class="k">{{ ART[x.t].n }}</span><span class="v">{{ x.from }} → {{ x.to }}</span>
            </div>
            <div v-if="!changesOf(selCommit).length" class="tree-line"><span class="df">（无变更，继承上一棵树）</span></div>
          </div>
          <div class="small" style="color: var(--tx-7); margin-bottom: 5px">完整树（该提交下所有制品）</div>
          <div class="tree-box" style="margin-bottom: 10px">
            <div v-for="t in ['prd', 'proto', 'ad', 'at', 'backlog', 'code']" :key="t" class="tree-line">
              <span class="k">{{ ART[t].n }}</span><span class="v">{{ (selCommit.tree && selCommit.tree[t]) || '—' }}</span>
              <span v-if="changesOf(selCommit).some((x) => x.t === t)" class="tag t-accent" style="font-size: 9px; padding: 0 4px">本次</span>
              <span class="df">衍生自 {{ (findVer(versions, t, selCommit.tree[t]) && findVer(versions, t, selCommit.tree[t]).df || []).map((d) => d.t.toUpperCase() + ' ' + d.v).join(' · ') || '—' }}</span>
            </div>
          </div>
          <div class="small" style="color: var(--tx-7); margin-bottom: 4px">交付四闸</div>
          <!-- 判不了的闸（unknown）用灰色问号，不能画成绿勾：
               绿勾说的是「查过了、没问题」，这里根本没查。 -->
          <div v-for="g in gates.all" :key="g.key" class="ck">
            <span class="dot" :style="'background:' + (g.unknown ? 'var(--bg-gray)' : (g.ok ? 'var(--bg-ok-soft4)' : 'var(--bg-err-soft3)') ) + ';color:' + (g.unknown ? 'var(--tx-7)' : (g.ok ? 'var(--ok)' : 'var(--err)'))">
              <span v-html="icon(g.unknown ? 'alert' : (g.ok ? 'check' : 'alert'), 10)" />
            </span>
            <span :style="{ color: g.unknown ? 'var(--tx-7)' : (g.ok ? 'var(--tx-4)' : 'var(--err)') }">{{ g.label }}</span>
            <span class="small muted" style="margin-left: auto">{{ g.detail }}</span>
          </div>
          <hr class="sep" />
          <div class="row">
            <span v-if="selCommit.tag" class="tag t-ok"><span v-html="icon('tag', 11)" /> {{ selCommit.tag }} 已交付</span>
            <button v-if="selCommit.tag" class="btn small" style="margin-left: auto" @click="S.view = 'orch'">去发布编排</button>
            <template v-else>
              <!-- 判不了的闸不算「通过」，单独说清楚是哪几条。 -->
              <span class="tag" :class="gates.all.length && gates.all.every((g) => g.ok || g.unknown) ? 't-ok' : 't-warn'">
                {{ gates.all.filter((g) => g.ok && !g.unknown).length }}/{{ gates.all.length }} 闸通过<template v-if="gates.all.some((g) => g.unknown)"> · {{ gates.all.filter((g) => g.unknown).length }} 判不了</template>
              </span>
              <span class="small muted">打 tag 由 SA 在发布页完成（审批 = 那条 publish.decided 事实）</span>
            </template>
          </div>
        </div>
        <div class="card">
          <div class="ch"><span v-html="icon('layers')" /><h3>制品版本（各自独立，不排队）</h3><span class="spacer" /><span class="small muted">方案 3 条 + 原型 + Backlog + 代码</span></div>
          <table class="tb">
            <thead><tr><th style="width: 76px">制品</th><th style="width: 74px">最新</th><th style="width: 62px">来源</th><th>衍生自</th><th style="width: 96px">状态</th></tr></thead>
            <tbody>
              <tr v-for="t in ['prd', 'proto', 'ad', 'at', 'backlog', 'code']" :key="t">
                <template v-if="latest(versions, t)">
                  <td><b style="font-weight: 550">{{ ART[t].n }}</b><div class="small muted">{{ (versions[t] || []).length }} 版</div></td>
                  <td class="mono" style="color: var(--info)">{{ latest(versions, t).v }}</td>
                  <td class="small muted">{{ latest(versions, t).source }}</td>
                  <td class="small muted">{{ (latest(versions, t).df || []).map((d) => d.t.toUpperCase() + ' ' + d.v).join(' · ') || '—' }}</td>
                  <td>
                    <span v-if="latest(versions, t).st === 'draft'" class="tag t-warn">草稿</span>
                    <span v-else-if="stale.some((x) => x.t === t)" class="tag t-err">过期</span>
                    <span v-else class="tag t-ok">已接受</span>
                    <div class="small muted" style="margin-top: 2px; font-size: 10px">{{ latest(versions, t).by || '—' }} · {{ latest(versions, t).at }}</div>
                  </td>
                </template>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>
  </template>

  <template v-else>
    <div class="card">
      <div class="ch">
        <span v-html="icon('doc')" /><h3>{{ ART[S.editArt].n }} · 文档</h3><span class="spacer" />
        <span class="tag" :class="dirty ? 't-warn' : latestArt.st === 'draft' ? 't-warn' : 't-ok'">
          {{ latestArt.v || '—' }} {{ latestArt.st === 'draft' ? '草稿' : '已接受' }}
        </span>
        <button class="btn small primary" style="margin-left: 8px" @click="saveDraft">保存草稿</button>
      </div>
      <div class="dc">
        <div class="dc-main">
          <div class="dc-top">
            <div class="dc-crumb"><span v-html="icon('box', 11)" /> <b>{{ unit.name }}</b> <span style="color: var(--tx-13)">/</span> <b>{{ ART[S.editArt].n }}</b></div>
            <span class="spacer" style="flex: 1" />
            <span class="small muted">{{ text.length }} 字</span>
            <span v-if="dirty" class="tag t-warn" style="margin-left: 8px">未保存</span>
          </div>
          <div class="dc-scroll" style="cursor: auto">
            <div class="dc-paper">
              <div class="dc-meta">
                <span>{{ latestArt.v }}</span><span style="color: var(--tx-13)">·</span>
                <span>{{ latestArt.by || '—' }}</span><span style="color: var(--tx-13)">·</span>
                <span>{{ latestArt.at || '—' }}</span>
              </div>
              <div class="dc-body" v-html="md2html(text)" />
            </div>
          </div>
          <div class="ed-bar">
            <span class="mono small" style="color: var(--info)">{{ S.editArt }}.md</span>
            <span class="spacer" style="flex: 1" />
            <input v-model="note" placeholder="保存说明（可选）" style="width: 200px; padding: 4px 8px; font-size: 11px" />
          </div>
          <textarea class="md-edit" spellcheck="false" :value="text" @input="onInput" />
          <div v-if="S.editArt === 'at'" class="small muted" style="padding: 7px 8px">
            每条验收用 AT-01 或 AC-01 编号，并写清 Given（前提）、When（操作）、Then（预期）。提交后由 BA 在验证页判定。
          </div>
        </div>
      </div>
      <div class="small muted" style="margin-top: 7px">
        保存会生成新的草稿版本（进工作区），提交由 workflow 完成——「保存」不等于「接受」，这是两条不同的账本事实。
        <button class="btn small" style="margin-left: 8px" @click="addAnnotationRound">加一条原型批注</button>
      </div>
    </div>
    <div class="card">
      <div class="ch"><span v-html="icon('git')" /><h3>{{ ART[S.editArt].n }} 版本线</h3><span class="spacer" /><span class="small muted">{{ (versions[S.editArt] || []).length }} 版</span></div>
      <div
        v-for="x in (versions[S.editArt] || []).slice().reverse()"
        :key="x.v"
        class="row"
        style="padding: 5px 0; border-bottom: 1px solid var(--line-e); font-size: 11.5px"
      >
        <span class="mono" style="color: var(--warn); width: 50px">{{ x.v }}</span>
        <span class="tag" :class="x.st === 'draft' ? 't-warn' : x.st === 'superseded' ? 't-gray' : 't-ok'" style="font-size: 9px">
          {{ x.st === 'draft' ? '草稿' : x.st === 'superseded' ? '已被覆盖' : '已接受' }}
        </span>
        <span class="muted" style="flex: 1">{{ x.note || '' }}</span>
        <span class="small muted">{{ x.by }} · {{ x.at }}</span>
      </div>
    </div>
  </template>
</template>
