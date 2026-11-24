<script setup>
import { computed } from 'vue';
import { S, unitKind, toast } from '../store.js';
import { icon } from '../icons.js';

// 单元 badge：pbc 显示 HEAD，agent 显示 A，hybrid 显示 P+A（原型 v2.3 同款）。
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

function go(v) {
  S.view = v;
}
function openUnit(id) {
  S.pbcId = id;
  const u = S.units.find((x) => x.id === id);
  if (u && u.head) S.selCommit = u.head;
  S.view = 'plan';
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

    <div class="sb-group">项目</div>
    <div class="trow" :class="{ on: S.view === 'projects' }" @click="go('projects')">
      <span v-html="icon('box')" /> 悦府物业 · 收费与工单数字化
    </div>
    <div class="trow sub" :class="{ on: S.view === 'brd' }" @click="go('brd')">
      <span v-html="icon('doc', 12)" /> BRD
      <span class="mono" style="color: var(--warn)">v{{ (S.brd && S.brd.v) || '—' }}</span>
      <span class="cnt">项目</span>
    </div>

    <template v-for="u in S.units" :key="u.id">
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
          <span v-if="m[0] === 'plan' && S.state && S.state.gates && S.state.gates.all && S.state.gates.all.some((g) => !g.ok)" class="cnt" style="color: var(--warn)">
            {{ S.state.gates.all.filter((g) => !g.ok).length }}
          </span>
        </div>
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
