<script setup>
import { computed } from 'vue';
import PageHeader from '../components/PageHeader.vue';
import { S, unitById } from '../store.js';
import { icon } from '../icons.js';

const tabs = [
  ['todo', '我的待办'],
  ['review', '待我确认'],
  ['mine', '我发起的'],
];

const list = computed(() => {
  const t = S.homeTab;
  if (t === 'review') return S.inbox.filter((x) => x.run === 'waiting');
  if (t === 'mine') return S.inbox.filter((x) => x.by === S.role);
  return S.inbox.filter(
    (x) => x.owner === S.role && x.st !== 'done' && x.run !== 'waiting' && x.run !== 'published',
  );
});

const busy = computed(() => S.agents.filter((a) => (a.st || []).includes('doing')));
const head = computed(() => (S.units.find((u) => u.id === S.pbcId) || {}).head || '—');
const tags = computed(() => S.units.flatMap((u) => u.tags || []));

function openItem(id) {
  S.itemId = id;
  S.view = 'item';
}
function unitName(id) {
  const u = unitById(id);
  return u ? u.name : id;
}
</script>

<template>
  <PageHeader
    :title="'工作台 · ' + (S.roleName || S.role)"
    :sub="'同一套界面，按角色看不同的活　·　我的待办 ' + S.inbox.filter((x) => x.owner === S.role && x.st !== 'done' && x.run !== 'waiting' && x.run !== 'published').length + ' · 待我确认 ' + S.inbox.filter((x) => x.run === 'waiting').length"
  />

  <div class="perm-note">
    <span v-html="icon('lock', 12)" />
    <span><b>权限口径：</b>你能做 {{ (S.acts || []).filter((a) => a !== 'view').join(' / ') || '—' }}；X 团队是 {{ S.agents.length }} 名专业 worker，产出物仍需人确认</span>
  </div>

  <div class="perm-note">
    <span v-html="icon('refresh', 12)" />
    <span>
      <b>演示数据说明：</b>启动时写入的历史由种子生成（actor 标为 <span class="mono">seed</span>），
      用来让页面有东西可看；<b>它不是真发生过的工作</b>。
      真跑出来的记录 actor 是 <span class="mono">x-fe / x-qa / x-ops / SA</span> 这些角色，
      测试条数是 <span class="mono">go test</span> 的真实输出，digest 是构建产物的内容摘要。
    </span>
  </div>

  <div class="home-tabs">
    <div
      v-for="t in tabs"
      :key="t[0]"
      class="home-tab"
      :class="{ on: S.homeTab === t[0] }"
      @click="S.homeTab = t[0]"
    >{{ t[1] }}</div>
  </div>

  <div v-if="!list.length" class="card">
    <div class="empty">这里暂时没有任务——{{ S.homeTab === 'review' ? '别人还没提交给你确认' : '活都干完了' }}</div>
  </div>
  <div v-else class="card">
    <div v-for="x in list" :key="x.pbc + '/' + x.id" class="task" @click="openItem(x.id)">
      <div class="ti" :style="x.run === 'waiting' ? 'background:var(--bg-warn-soft);color:var(--warn)' : ''" v-html="icon(x.run === 'waiting' ? 'check' : 'box', 12)" />
      <div class="tt">
        <b>{{ x.t }}</b>
        <span>{{ x.id }} · {{ unitName(x.pbc) }} · {{ x.kind }}{{ x.by ? ' · 由 ' + x.by + ' 提' : '' }}{{ x.run === 'waiting' ? ' · 全链已跑通，待你拍板' : '' }}</span>
      </div>
      <span class="due">{{ x.at || '—' }}</span>
      <span class="tag" :class="x.run === 'waiting' ? 't-warn' : 't-gray'">{{ x.run === 'waiting' ? '待发布' : '待处理' }}</span>
    </div>
  </div>

  <div class="grid2">
    <div class="card">
      <div class="ch">
        <span v-html="icon('git')" /><h3>项目进度</h3><span class="spacer" /><span class="small muted">悦府物业 · 收费与工单数字化</span>
      </div>
      <div class="tree-box">
        <div class="tree-line"><span class="k">BRD</span><span class="v">{{ (S.brd && S.brd.v) || '—' }} · 草稿 {{ S.brd ? (S.brd.versions || []).filter((v) => v.st === 'draft').length : 0 }} 项</span></div>
        <div class="tree-line"><span class="k">交付单元</span><span class="v">{{ S.units.length }} 个 · 当前 HEAD {{ head }}</span></div>
        <div class="tree-line"><span class="k">交付</span><span class="v">{{ tags.join(' / ') || '—' }}</span></div>
        <div class="tree-line"><span class="k">账本</span><span class="v">seq {{ (S.state && S.state.seq) || 0 }} 条事实</span></div>
      </div>
    </div>
    <div class="card">
      <div class="ch">
        <span v-html="icon('robot')" /><h3>数字员工</h3><span class="spacer" />
        <span class="tag t-purple">在岗 {{ busy.length }} / {{ S.agents.length }}</span>
      </div>
      <div class="small muted">在办：{{ busy.length ? busy.map((w) => w.n).join('、') : '暂无' }}</div>
      <div class="small muted" style="margin-top: 6px">X 是独立团队，不占真人角色；花名册与执行详情在「Agent 工厂」。</div>
      <div class="row" style="margin-top: 9px"><button class="btn small" @click="S.view = 'workers'">打开 Agent 工厂</button></div>
    </div>
  </div>
</template>
