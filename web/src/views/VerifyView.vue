<script setup>
// VerifyView —— 真人验收台（原型 v2.5 / v2.6）。
//
// 这一步不是看报告，是人自己上手跑一遍：照着清单操作、判过不过，
// 不过就提 bug。机器自检（四闸 / 评测台 / 知识库准确度）已经挪到
// 构建页的「质量报告」——那是构建结果的查看，不算验收。
import { computed, ref, watch } from 'vue';
import PageHeader from '../components/PageHeader.vue';
import AcceptTerminal from '../components/AcceptTerminal.vue';
import { S, unitKind, currentAgent, currentUnit, toast, refresh } from '../store.js';
import { api } from '../api.js';
import { icon } from '../icons.js';
import { kindCls, kindText } from '../derive.js';

const kind = computed(() => unitKind());
const unit = computed(() => currentUnit() || {});
const agent = computed(() => currentAgent());
const checks = computed(() => (S.state && S.state.checks) || []);
const defects = computed(() => (S.state && S.state.defects) || []);
const chats = computed(() => (S.state && S.state.chats) || []);
const can = (a) => (S.acts || []).includes(a);

const idx = computed(() => Math.min(S.vIdx || 0, Math.max(0, checks.value.length - 1)));
const cur = computed(() => checks.value[idx.value] || null);
const done = computed(() => checks.value.filter((c) => c.st !== 'todo').length);
const passN = computed(() => checks.value.filter((c) => c.st === 'pass').length);

const terminal = ref(null);
const bugTitle = ref('');
const bugSev = ref('一般');
const bugSteps = ref('');

// v3.2：打 tag 挪到验证页末尾。tag 是发布编排唯一认的东西——
// 没有 tag 的单元不会出现在批次里，也不会被「顺手带上」。
const head = computed(() => (S.state && S.state.commits && S.state.commits[0]) || null);
const unitTag = computed(() => (head.value && head.value.tag) || '');
const testsOK = computed(() => {
  const t = S.state && S.state.test;
  return !!(t && head.value && t.commit === head.value.no && t.pass === t.total);
});
const gatesOK = computed(() => {
  const gs = (S.state && S.state.gates && S.state.gates.all) || [];
  return gs.length > 0 && gs.every((g) => g.ok || g.unknown);
});
const checksPass = computed(() => checks.value.length > 0 && checks.value.every((c) => c.st === 'pass'));
const remainAccept = computed(() => checks.value.filter((c) => c.st !== 'pass').length);
// 没有验收清单时以机器测试与闸门为准——但页面要说清这件事，不假装「全过」。
const tagReason = computed(() => {
  if (!head.value) return '还没有提交';
  if (!head.value.digest) return 'HEAD 还没构建';
  if (!testsOK.value) return '机器测试未全过';
  if (!gatesOK.value) return '闸门未过';
  if (remainAccept.value) return '还差 ' + remainAccept.value + ' 条验收未通过';
  return '';
});
const canTag = computed(() => !unitTag.value && !tagReason.value);

async function doTag() {
  try {
    const j = await api.tagUnit(S.pbcId);
    toast('已打 tag ' + j.tag + ' · 进「发布编排」池', 'ok');
  } catch (e) {
    toast(e.message, 'err');
  }
  await refresh();
  await loadReplay(unitTag.value);
}

// 回放：这个 tag 打下去的那一刻，账本是什么样、闸依据是什么。
//
// 它只读 /replay，并且允许失败：老账本没有 gate.evaluated 时接口照样 200，
// 只是 gatesNote 说明「当时没记录闸门」。失败就说失败，不拿今天的闸顶上。
const replay = ref(null);
const replayErr = ref('');
const replayOpen = ref(false);
async function loadReplay(tag) {
  replay.value = null;
  replayErr.value = '';
  if (!tag) return;
  try {
    replay.value = await api.replay(S.pbcId, tag);
  } catch (e) {
    replayErr.value = e.message;
  }
}
watch(
  () => [S.pbcId, unitTag.value],
  ([id, t]) => {
    if (id && t) loadReplay(t);
  },
  { immediate: true },
);
function gateText(g) {
  if (g.unknown) return '判不了';
  return g.ok ? '通过' : '未通过';
}

function sel(i) {
  S.vIdx = i;
  S.vBugOpen = false;
}
function markCls(st) {
  return st === 'pass' ? 'pass' : st === 'fail' ? 'fail' : 'todo';
}
function markText(st) {
  return st === 'pass' ? '✓' : st === 'fail' ? '✕' : '○';
}
function stText(st) {
  return st === 'pass' ? '已通过' : st === 'fail' ? '不通过' : '待验';
}

async function pass() {
  const c = cur.value;
  if (!c) return;
  try {
    await api.checkDecide(c.id, { st: 'pass' }, S.pbcId);
    toast(c.id + ' 判定：通过', 'ok');
  } catch (e) {
    toast(e.message, 'err');
  }
  await refresh();
}

function openBug() {
  const c = cur.value;
  if (!c) return;
  bugTitle.value = c.n + '：' + String(c.t || '').slice(0, 12);
  bugSev.value = '一般';
  // 复现步骤自动带入刚才的操作，不用手写。
  bugSteps.value = terminal.value && terminal.value.repro ? terminal.value.repro : '';
  S.vBugOpen = true;
}

async function submitBug() {
  const c = cur.value;
  if (!c) return;
  const t = bugTitle.value.trim();
  if (!t) {
    toast('先写清 bug 标题', 'err');
    return;
  }
  try {
    const j = await api.checkDecide(
      c.id,
      { st: 'fail', title: t, sev: bugSev.value, steps: bugSteps.value },
      S.pbcId,
    );
    S.vBugOpen = false;
    toast('已提 bug：' + t + '（复现步骤已自动带入）', 'ok');
    if (j.defect) pushDefect(j.defect);
  } catch (e) {
    toast(e.message, 'err');
  }
  await refresh();
}

function pushDefect(d) {
  // 通知后端已经落账，这里只是让右侧列表立刻有反馈；刷新后以投影为准。
  if (d) toast('bug 已进 backlog，由 X 领活修', 'ok');
}
</script>

<template>
  <PageHeader
    :title="'验证 <span class=&quot;small&quot; style=&quot;color:var(--ok)&quot;>' + done + '/' + checks.length + ' 已验 · 通过 ' + passN + '</span>'"
    sub="这一步不是看报告，是人自己上手跑一遍：照着清单操作、判过不过，不过就提 bug。机器自检在构建页的「质量报告」里。"
  >
    <button class="btn small" @click="S.view = 'build'"><span v-html="icon('check', 12)" /> 看机器自检</button>
  </PageHeader>

  <div class="unitbar">
    <span class="tag" :class="kindCls(kind)" style="font-size: 9px">{{ kindText(kind) }}</span>
    <span class="muted">{{ unit.name || '' }} · 验收清单 {{ checks.length }} 条</span>
  </div>

  <div v-if="!can('acceptAT')" class="perm-note">
    <span v-html="icon('lock', 12)" /> 验收判定是 <b>BA</b> 的职责（acceptAT），当前角色只能看
  </div>

  <div class="row wrap" style="gap: 6px; margin-bottom: 9px; font-size: 11px">
    <span class="tag t-info" style="font-size: 9px">验收环境 test</span>
    <span class="small muted">以 {{ S.roleName || S.role }} 身份验收</span>
    <span class="small muted">· 会话 s-{{ S.pbcId }}-0924</span>
    <span class="spacer" style="flex: 1" />
    <span class="small muted">你的每一步操作都会记进复现步骤</span>
  </div>

  <div v-if="!checks.length" class="card">
    <div class="empty">这个单元还没有验收清单。清单随 AT 制品进账本，没有清单就没有「验过」这回事。</div>
  </div>

  <template v-else>
    <div class="card" style="margin-bottom: 9px">
      <div class="ac-bar">
        <button
          v-for="(c, i) in checks"
          :key="c.id"
          class="ac-chip"
          :class="[markCls(c.st), { on: i === idx }]"
          @click="sel(i)"
        >
          <span class="m">{{ markText(c.st) }}</span>{{ c.id }} {{ c.n }}
        </button>
      </div>
    </div>

    <div style="display: grid; grid-template-columns: 1fr 234px; gap: 9px; align-items: start">
      <AcceptTerminal ref="terminal" :kind="kind" :unit="S.pbcId" :agent="agent" :chats="chats" />

      <div class="card" style="margin: 0">
        <div class="ch" style="padding: 8px 10px">
          <h3 style="font-size: 12px">{{ cur.id }}</h3>
          <span class="spacer" />
          <span class="tag" :class="cur.st === 'pass' ? 't-ok' : cur.st === 'fail' ? 't-err' : 't-gray'" style="font-size: 9px">{{ stText(cur.st) }}</span>
        </div>
        <div style="padding: 0 10px 10px">
          <div style="font-size: 11.5px; font-weight: 600; color: var(--tx-2); margin-bottom: 7px">{{ cur.n }}</div>
          <div class="tree-box" style="margin: 0; font-size: 10.5px">
            <div class="tree-line"><span class="k" style="width: 34px">Given</span><span class="v">{{ cur.g }}</span></div>
            <div class="tree-line"><span class="k" style="width: 34px">When</span><span class="v">{{ cur.w }}</span></div>
            <div class="tree-line"><span class="k" style="width: 34px">Then</span><span class="v">{{ cur.t }}</span></div>
          </div>
          <div class="row" style="gap: 5px; margin-top: 9px">
            <button class="btn small primary" style="flex: 1" :disabled="!can('acceptAT')" @click="pass">
              <span v-html="icon('check', 11)" /> 通过
            </button>
            <button class="btn small" style="flex: 1; color: var(--err)" :disabled="!can('acceptAT')" @click="openBug">
              <span v-html="icon('alert', 11)" /> 不提过
            </button>
          </div>
          <div class="small muted" style="margin-top: 7px; line-height: 1.6">不通过就提 bug：复现步骤会把你刚才的操作带进去，不用手写。</div>

          <div v-if="S.vBugOpen" style="margin-top: 9px; padding-top: 9px; border-top: 1px dashed var(--line-i)">
            <div class="small muted" style="margin-bottom: 4px">bug 标题</div>
            <input v-model="bugTitle" class="bug-title" style="font-size: 11px; margin-bottom: 6px; width: 100%" />
            <div class="small muted" style="margin-bottom: 4px">严重级</div>
            <select v-model="bugSev" class="bug-sev" style="font-size: 11px; margin-bottom: 8px; width: 100%">
              <option>阻塞</option><option>严重</option><option>一般</option><option>建议</option>
            </select>
            <div class="small muted" style="margin-bottom: 4px">复现步骤（自动带入）</div>
            <textarea v-model="bugSteps" class="bug-steps" style="font-size: 10.5px; height: 56px; width: 100%; margin-bottom: 8px" />
            <div class="row" style="gap: 5px">
              <button class="btn small primary" style="flex: 1" @click="submitBug">提交</button>
              <button class="btn small" @click="S.vBugOpen = false">取消</button>
            </div>
          </div>
        </div>
      </div>
    </div>

    <div class="card">
      <div class="ch"><span v-html="icon('alert')" /><h3>本轮提的 bug</h3><span class="spacer" />
        <span class="small muted">{{ defects.length }} 条 · 会进 backlog 由 X 修</span>
      </div>
      <div v-if="!defects.length" class="small muted">还没有提 bug。不通过时点「不提过」，复现步骤自动带入。</div>
      <div
        v-for="d in defects"
        :key="d.id"
        class="row"
        style="padding: 6px 0; border-bottom: 1px solid var(--line-e); gap: 8px; align-items: flex-start"
      >
        <span class="mono small" style="color: var(--err)">{{ d.id }}</span>
        <span class="small" style="flex: 1">{{ d.t }}</span>
        <span class="small muted mono">{{ d.ac }}</span>
        <span class="tag" :class="d.sev === '阻塞' ? 't-err' : d.sev === '严重' ? 't-warn' : 't-gray'" style="font-size: 9px">{{ d.sev }}</span>
      </div>
      <div class="small muted" style="margin-top: 8px">验收不通过 ≠ 打回重做。bug 进 backlog，修完这一条重验，其它条不受影响。</div>
    </div>


  </template>

    <!-- v3.2：打 tag 是验证页的收口。打完进「发布编排」池，上环境在那边定。 -->
    <div class="card">
      <div class="ch">
        <span v-html="icon('tag')" /><h3>交付物</h3>
        <span class="spacer" />
        <span class="small muted">{{ head ? ('HEAD ' + head.no + (head.digest ? ' · ' + head.digest.slice(0, 21) + '…' : '')) : '—' }}</span>
      </div>
      <div v-if="unitTag" class="row" style="gap: 8px; align-items: center">
        <span class="tag t-ok" style="font-size: 9px">{{ unitTag }} 已交付</span>
        <span class="small muted">已进「发布编排」池；上哪个环境、和谁一起上，在项目级定。</span>
        <span class="spacer" style="flex: 1" />
        <button class="btn small" @click="S.view = 'orch'"><span v-html="icon('layers', 11)" /> 去发布编排</button>
      </div>
      <template v-else>
        <div class="row" style="gap: 8px; align-items: center">
          <span v-if="tagReason" class="tag t-warn" style="font-size: 9px">{{ tagReason }}</span>
          <span v-else class="tag t-ok" style="font-size: 9px">验收全过，可以打 tag</span>
          <span v-if="!checks.length" class="small muted">这个单元还没有验收清单，打 tag 以机器测试与闸门为准。</span>
          <span class="spacer" style="flex: 1" />
          <button class="btn small primary" :disabled="!canTag || !can('tag')" @click="doTag">
            <span v-html="icon('tag', 11)" /> 打 tag 固化交付物
          </button>
        </div>
        <div class="small muted" style="margin-top: 6px">tag 是发布编排唯一认的东西：没有 tag 的单元不会进批次。</div>
      </template>

      <!-- 回放：打在 tag.created 那一刻的账本上。读的是当时落账的闸，
           不是按今天的账本重算一遍——那算的是现在，不是当时。 -->
      <div v-if="replay" class="replay-box">
        <div class="row" style="align-items: center; gap: 8px">
          <span class="tag t-info" style="font-size: 9px">回放 {{ replay.tag }}</span>
          <span class="small muted">取自 seq {{ replay.seq }} · {{ replay.at }} · 证据 {{ replay.facts.length }} 条</span>
          <span class="spacer" style="flex: 1" />
          <button class="btn small" @click="replayOpen = !replayOpen">
            {{ replayOpen ? '收起依据' : '看依据' }}
          </button>
        </div>
        <div class="small muted" style="margin-top: 5px">
          这一刻 HEAD 是 {{ replay.commit }}，测试 {{ replay.test ? (replay.test.pass + '/' + replay.test.total) : '没有记录' }}；
          验收 {{ replay.checks.length ? (replay.checks.filter((c) => c.st === 'pass').length + '/' + replay.checks.length + ' 通过') : '无清单' }}。
        </div>
        <div v-if="replay.gatesNote" class="small" style="margin-top: 6px; color: var(--warn)">{{ replay.gatesNote }}</div>
        <div v-else class="replay-gates">
          <span v-for="g in replay.gates" :key="g.key" class="tag" :class="g.ok && !g.unknown ? 't-ok' : g.unknown ? 't-gray' : 't-err'" style="font-size: 9px">
            {{ g.label }} · {{ gateText(g) }}
          </span>
        </div>
        <div v-if="replayOpen" class="replay-facts">
          <div v-for="f in replay.facts" :key="f.seq" class="replay-fact">
            <span class="mono small muted">#{{ f.seq }}</span>
            <span class="tag t-gray" style="font-size: 9px">{{ f.type }}</span>
            <span class="small muted">{{ f.actor }} · {{ f.at }}</span>
          </div>
        </div>
      </div>
    </div>
</template>