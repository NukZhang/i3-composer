<script setup>
import { computed } from 'vue';
import { S, unitKind, toast, unitsOfProject } from '../store.js';
import { icon } from '../icons.js';

// 单元 badge：pbc 显示 HEAD，agent 显示 A，hybrid 显示 P+A（原型同款）。
function badge(u) {
  const k = u.kind || 'pbc';
  if (k === 'agent') return 'A';
  if (k === 'hybrid') return 'P+A';
  return u.head || '—';
}
function badgeColor(k) {
  return k === 'agent' ? 'var(--purple)' : k === 'hybrid' ? 'var(--accent)' : 'var(--tx-9)';
}

const todoCount = computed(
  () => S.inbox.filter((x) => x.run === 'waiting' || (x.owner === S.role && x.st !== 'done' && x.run !== 'waiting' && x.run !== 'published')).length,
);
const unread = computed(() => S.notifications.length);
const STAGES = [
  ['plan', '方案', 'git'],
  ['proto', '原型', 'layout'],
  ['build', '构建', 'cpu'],
  ['verify', '验证', 'check'],
  ['release', '发布', 'cloud'],
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
function add() {
  S.addOpen = true;
  S.addTab = 'new';
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
      <span class="sb-add" title="新建项目 / 工作空间，或加入已有" @click="add">+</span>
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
            :class="{ on: S.projId === p.id }"
            :title="p.owner || ''"
            style="padding-left:18px"
            @click="openProject(p.id)"
          >
            <span v-html="icon('box', 12)" /> {{ p.name }}
            <span v-if="p.shared" class="cnt" style="color: var(--info)">共享</span>
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
                :class="{ on: S.pbcId === u.id }"
                :title="'v' + (u.head || '—') + ' · ' + (u.kind === 'agent' ? 'Agent' : u.kind === 'hybrid' ? 'PBC + Agent' : 'PBC')"
                @click="openUnit(u.id)"
              >
                <span v-html="icon(u.kind === 'agent' ? 'robot' : 'layers', 12)" /> {{ u.name }}
                <span class="cnt" :style="{ color: badgeColor(u.kind), fontWeight: 600 }">{{ badge(u) }}</span>
              </div>
              <template v-if="S.pbcId === u.id">
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
        <div class="trow sub" :class="{ on: S.projId === p.id }" :title="p.owner || ''" style="padding-left:18px" @click="openProject(p.id)">
          <span v-html="icon('box', 12)" /> {{ p.name }}
          <span v-if="p.shared" class="cnt" style="color: var(--info)">共享</span>
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
              :class="{ on: S.pbcId === u.id }"
              :title="'v' + (u.head || '—')"
              @click="openUnit(u.id)"
            >
              <span v-html="icon(u.kind === 'agent' ? 'robot' : 'layers', 12)" /> {{ u.name }}
              <span class="cnt" :style="{ color: badgeColor(u.kind), fontWeight: 600 }">{{ badge(u) }}</span>
            </div>
            <template v-if="S.pbcId === u.id">
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
