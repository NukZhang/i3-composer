<script setup>
import { computed } from 'vue';
import { S, unitKind, toast, unitsOfProject, refresh } from '../store.js';
import { icon } from '../icons.js';

// 单元 badge：pbc 显示 HEAD；agent 显示「产物」——它是交付出去的东西，
// 不是平台员工（员工不对外交付，也不单独打 tag）。
function badge(u) {
  const k = u.kind || 'pbc';
  if (k === 'agent') return '产物';
  if (k === 'hybrid') return 'P+A';
  return u.head || '—';
}
function badgeColor(k) {
  return k === 'agent' ? 'var(--purple)' : k === 'hybrid' ? 'var(--accent)' : 'var(--tx-9)';
}

const todoCount = computed(
  // 失败的工作项要人看见并重来：它也算「待我处理」（#53）。
  () => S.inbox.filter((x) => x.run === 'waiting' || x.run === 'failed' || (x.owner === S.role && x.st !== 'done' && x.run !== 'waiting' && x.run !== 'tagged')).length,
);
const unread = computed(() => S.notifications.length);
// v3.2：单元只剩四个阶段。发布被提到项目级——「发布编排」不是第五个阶段，
// 它是对整批单元做的动作，入口挂在项目行上。
const STAGES = [
  ['plan', '方案', 'git'],
  ['proto', '原型', 'layout'],
  ['build', '构建', 'cpu'],
  ['verify', '验证', 'check'],
];

// 两级结构：工作空间（文件夹）→ 项目。项目可以不属于任何工作空间。
const grouped = computed(() =>
  S.workspaces.map((w) => ({ w, projects: S.projects.filter((p) => p.ws === w.id) })),
);
const solo = computed(() => S.projects.filter((p) => !p.ws));
const currentProject = computed(() => S.projects.find((p) => p.id === S.projId) || null);
const curUnits = computed(() => unitsOfProject(S.projId));

function open(id) {
  S.wsOpen[id] = !(S.wsOpen[id] !== false);
}
function isOpen(id) {
  return S.wsOpen[id] !== false; // 默认展开
}
function go(v) {
  S.view = v;
}
function openProject(id) {
  S.projId = id;
  const p = S.projects.find((x) => x.id === id);
  S.wsId = (p && p.ws) || '';
  const mine = unitsOfProject(id);
  S.pbcId = mine.length ? mine[0].id : '';
  S.view = 'projects';
}
function openUnit(id) {
  S.pbcId = id;
  const u = S.units.find((x) => x.id === id);
  if (u && u.proj) {
    S.projId = u.proj;
    const p = S.projects.find((x) => x.id === u.proj);
    S.wsId = (p && p.ws) || '';
  }
  if (u && u.head) S.selCommit = u.head;
  S.view = 'plan';
}
// v3.1：发布编排是项目级动作，入口是项目行右侧的图标，不占单元列表的一行。
// 点项目名进项目页，点图标进编排——两个动作分开。
function openOrch(pid) {
  S.projId = pid;
  const mine = unitsOfProject(pid);
  S.pbcId = mine.length ? mine[0].id : '';
  S.view = 'orch';
  refresh();
}
// 图标颜色即状态：灰 = 还没有单元；黄 = 有单元待交付；绿 = 全部就绪。
function orchState(pid) {
  const us = unitsOfProject(pid);
  if (!us.length) return 'none';
  return us.every((u) => u.headTag) ? 'ready' : 'pending';
}
function orchTitle(pid) {
  const us = unitsOfProject(pid);
  if (!us.length) return '发布编排 · 还没有交付单元';
  const pending = us.filter((u) => !u.headTag).length;
  return pending ? '发布编排 · ' + pending + ' 待交付' : '发布编排 · 全部就绪';
}
function orchColor(pid) {
  const st = orchState(pid);
  return st === 'ready' ? 'var(--ok)' : st === 'pending' ? 'var(--warn)' : 'var(--tx-9)';
}
// 「＋」弹窗里的写操作（建项目 / 建工作空间）都是 PA 的 ActPbc；「加入已有」谁都能做。
const canAdd = computed(() => (S.acts || []).includes('pbc'));
function add() {
  S.addOpen = true;
  // 没有 ActPbc 的人一打开就落在「加入已有」这页：那里才是他真能做的事（#36）。
  S.addTab = canAdd.value ? 'new' : 'join';
  S.addKind = 'proj';
}
function grayModule(name) {
  toast(name + ' 模块：规划中（本演示只做「构建」）', 'warn');
}
</script>

<template>
  <nav class="sidebar" id="sidebar">
    <div class="sb-group">我的工作</div>
    <div class="trow" :class="{ on: S.view === 'home' }" @click="go('home')">
      <span v-html="icon('box')" /> 工作台
      <span v-if="todoCount" class="badge">{{ todoCount }}</span>
    </div>

    <div class="sb-group">
      项目
      <!-- 新建 / 加入弹窗里能做的写操作（建项目、建工作空间）都是 PA 的 ActPbc。
           没有它的人仍然能打开弹窗加入已有项目，所以入口不藏；但标题先说是谁的活（#36）。 -->
      <span
        class="sb-add"
        :title="canAdd ? '新建项目 / 工作空间，或加入已有' : '新建项目 / 工作空间是 PA 的职责；这里仍可加入已有项目'"
        @click="add"
      >+</span>
    </div>

    <!-- 工作空间 → 项目 -->
    <template v-for="g in grouped" :key="g.w.id">
      <div v-if="g.projects.length || g.w.shared" class="sb-ws" @click="open(g.w.id)">
        <span class="ar2" :style="isOpen(g.w.id) ? 'transform:rotate(90deg)' : ''" />
        <span style="flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">{{ g.w.name }}</span>
        <span class="cnt">{{ g.projects.length }}</span>
      </div>
      <template v-if="isOpen(g.w.id)">
        <template v-for="p in g.projects" :key="p.id">
          <div
            class="trow sub"
            :class="{ on: S.projId === p.id && S.view !== 'orch' }"
            :title="p.owner || ''"
            style="padding-left:18px"
            @click="openProject(p.id)"
          >
            <span v-html="icon('box', 12)" /> {{ p.name }}
            <span v-if="p.shared" class="cnt" style="color: var(--info)">共享</span>
            <button
              class="orch-ico"
              :class="{ on: S.view === 'orch' && S.projId === p.id }"
              :style="{ color: orchColor(p.id) }"
              :title="orchTitle(p.id)"
              @click.stop="openOrch(p.id)"
            ><span v-html="icon('layers', 12)" /></button>
          </div>
          <template v-if="S.projId === p.id">
            <div class="trow sub" :class="{ on: S.view === 'brd' }" @click="go('brd')">
              <span v-html="icon('doc', 12)" /> BRD
              <span v-if="p.brd" class="mono" style="color: var(--warn)">{{ p.brd.v }}</span>
              <span v-else class="cnt" style="color: var(--warn)">待建</span>
            </div>
            <template v-for="u in curUnits" :key="u.id">
              <div
                class="trow sub"
                :class="{ on: S.pbcId === u.id && S.view !== 'orch' }"
                :title="'v' + (u.head || '—') + ' · ' + (u.kind === 'agent' ? 'Agent' : u.kind === 'hybrid' ? 'PBC + Agent' : 'PBC')"
                :data-unit="u.id"
                @click="openUnit(u.id)"
              >
                <span v-html="icon(u.kind === 'agent' ? 'robot' : 'layers', 12)" /> {{ u.name }}
                <span class="cnt" :style="{ color: badgeColor(u.kind), fontWeight: 600 }">{{ badge(u) }}</span>
              </div>
              <template v-if="S.pbcId === u.id && S.view !== 'orch'">
                <div
                  v-for="m in STAGES"
                  :key="m[0]"
                  class="trow sub2"
                  :class="{ on: S.view === m[0] }"
                  @click="go(m[0])"
                >
                  <span v-html="icon(m[2], 11)" /> {{ m[1] }}
                  <span v-if="m[0] === 'plan' && S.state && S.state.gates && S.state.gates.all && S.state.gates.all.some((g2) => !g2.ok)" class="cnt" style="color: var(--warn)">
                    {{ S.state.gates.all.filter((g2) => !g2.ok).length }}
                  </span>
                </div>
              </template>
            </template>
            <div v-if="!curUnits.length" class="trow sub2" style="cursor: default; color: var(--tx-12)">还没有交付单元</div>
          </template>
        </template>
        <div v-if="!g.projects.length" class="trow sub2" style="cursor: default; color: var(--tx-12)">还没有项目</div>
      </template>
    </template>

    <!-- 未归入工作空间的项目 -->
    <template v-if="solo.length">
      <div class="sb-solo">未归入工作空间</div>
      <template v-for="p in solo" :key="p.id">
        <div class="trow sub" :class="{ on: S.projId === p.id && S.view !== 'orch' }" :title="p.owner || ''" style="padding-left:18px" @click="openProject(p.id)">
          <span v-html="icon('box', 12)" /> {{ p.name }}
          <span v-if="p.shared" class="cnt" style="color: var(--info)">共享</span>
          <button
            class="orch-ico"
            :class="{ on: S.view === 'orch' && S.projId === p.id }"
            :style="{ color: orchColor(p.id) }"
            :title="orchTitle(p.id)"
            @click.stop="openOrch(p.id)"
          ><span v-html="icon('layers', 12)" /></button>
        </div>
        <template v-if="S.projId === p.id">
          <div class="trow sub" :class="{ on: S.view === 'brd' }" @click="go('brd')">
            <span v-html="icon('doc', 12)" /> BRD
            <span v-if="p.brd" class="mono" style="color: var(--warn)">{{ p.brd.v }}</span>
            <span v-else class="cnt" style="color: var(--warn)">待建</span>
          </div>
          <template v-for="u in unitsOfProject(p.id)" :key="u.id">
            <div
              class="trow sub"
              :class="{ on: S.pbcId === u.id && S.view !== 'orch' }"
              :title="'v' + (u.head || '—')"
              :data-unit="u.id"
              @click="openUnit(u.id)"
            >
              <span v-html="icon(u.kind === 'agent' ? 'robot' : 'layers', 12)" /> {{ u.name }}
              <span class="cnt" :style="{ color: badgeColor(u.kind), fontWeight: 600 }">{{ badge(u) }}</span>
            </div>
            <template v-if="S.pbcId === u.id && S.view !== 'orch'">
              <div v-for="m in STAGES" :key="m[0]" class="trow sub2" :class="{ on: S.view === m[0] }" @click="go(m[0])">
                <span v-html="icon(m[2], 11)" /> {{ m[1] }}
              </div>
            </template>
          </template>
          <div v-if="!unitsOfProject(p.id).length" class="trow sub2" style="cursor: default; color: var(--tx-12)">还没有交付单元</div>
        </template>
      </template>
    </template>

    <div class="sb-group">运行</div>
    <div class="trow" :class="{ on: S.view === 'notify' }" @click="go('notify')">
      <span v-html="icon('bell')" /> 通知中心
      <span v-if="unread" class="badge">{{ unread }}</span>
    </div>
    <div class="trow" :class="{ on: S.view === 'tech' }" @click="go('tech')">
      <span v-html="icon('cpu')" /> 技术架构
    </div>

    <div class="sb-group">平台模块</div>
    <div class="trow" :class="{ on: S.view === 'config' }" @click="go('config')">
      <span v-html="icon('plug')" /> 配置<span class="cnt">SA</span>
    </div>
    <div v-for="m in ['蓝图', '运营']" :key="m" class="trow lock" @click="grayModule(m)">
      <span v-html="icon('lock')" /> {{ m }}<span class="cnt">规划</span>
    </div>
  </nav>
</template>
