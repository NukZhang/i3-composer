<script setup>
import { onMounted, onUnmounted, ref, computed, watch } from 'vue';
import { S, refresh, connect, toast } from './store.js';
import { api } from './api.js';
import { icon } from './icons.js';
import SideBar from './components/Sidebar.vue';
import RightPane from './components/RightPane.vue';
import HomeView from './views/HomeView.vue';
import ProjectsView from './views/ProjectsView.vue';
import PlanView from './views/PlanView.vue';
import ProtoView from './views/ProtoView.vue';
import BuildView from './views/BuildView.vue';
import VerifyView from './views/VerifyView.vue';
import OrchView from './views/OrchView.vue';
import WorkersView from './views/WorkersView.vue';
import AgentView from './views/AgentView.vue';
import ItemView from './views/ItemView.vue';
import BRDView from './views/BRDView.vue';
import NotifyView from './views/NotifyView.vue';
import TechView from './views/TechView.vue';
import ConfigView from './views/ConfigView.vue';
import AddProjectModal from './components/AddProjectModal.vue';

const VIEWS = {
  home: HomeView,
  projects: ProjectsView,
  plan: PlanView,
  proto: ProtoView,
  build: BuildView,
  verify: VerifyView,
  orch: OrchView,
  workers: WorkersView,
  agent: AgentView,
  item: ItemView,
  brd: BRDView,
  notify: NotifyView,
  tech: TechView,
  config: ConfigView,
};

const current = computed(() => VIEWS[S.view] || ProjectsView);
const light = ref(false);
const ROLE_IDS = ['BA', 'PA', 'DA', 'SA'];

function toggleTheme() {
  light.value = !light.value;
  document.body.classList.toggle('light', light.value);
  try {
    localStorage.setItem('i3-theme', light.value ? 'light' : 'dark');
  } catch {
    /* 隐私模式：忽略 */
  }
}

async function setRole(id) {
  try {
    const j = await api.setRole(id);
    S.role = j.role;
    // /api/role 的响应里已经带了新角色的 acts：当场写进去，别等 refresh() 拉完账本才解锁按钮（#46）。
    // 否则账本一大，「我是谁」已经变了而「我能干什么」还要等近 1 秒。
    S.roleName = j.name;
    S.acts = j.acts || [];
    toast('已切换为 ' + j.name + ' · 只显示与你有关的待办与确认', 'ok');
  } catch (e) {
    toast(e.message, 'err');
  }
  await refresh();
}

function grayModule(name) {
  toast(name + '模块：规划中（本演示只做「构建」）', 'warn');
}

// 切换交付单元必须重拉投影：每个单元的 kind / versions / gates 都不同，
// 只改 pbcId 不刷新的话，页面会拿着上一个单元的账本渲染——那就是第二份真相。
watch(
  () => S.pbcId,
  (id, prev) => {
    if (id !== prev && (id || prev)) refresh();
  },
);

// v2.7：空项目没有当前单元。凡是「必须先有交付单元」的视图，
// 直接回项目页——不让它渲染空白再报错（原型 §五 的容错）。
const STAGE_VIEWS = ['plan', 'proto', 'build', 'verify', 'agent', 'item'];
watch(
  () => [S.view, S.pbcId],
  () => {
    if (!S.pbcId && STAGE_VIEWS.includes(S.view)) S.view = 'projects';
  },
  { immediate: true },
);

let closeStream = null;
onMounted(() => {
  try {
    light.value = localStorage.getItem('i3-theme') === 'light';
  } catch {
    /* 忽略 */
  }
  document.body.classList.toggle('light', light.value);
  // 本地快照必须停用：否则它会掩盖后端真实状态。
  try {
    localStorage.removeItem('i3:snap');
  } catch {
    /* 忽略 */
  }
  refresh();
  closeStream = connect();
});
onUnmounted(() => closeStream && closeStream());
</script>

<template>
  <div class="topbar">
    <div class="brand">
      <span class="logo">i3</span>
      <span>i3<small>构建工作台 · 编辑 → 提交 → 构建 → 交付</small></span>
    </div>
    <nav class="mods">
      <button class="mod lock" @click="grayModule('蓝图')">蓝图</button>
      <button class="mod active">构建</button>
      <button class="mod lock" @click="grayModule('运营')">运营</button>
      <button class="mod lock" @click="grayModule('配置')">配置</button>
    </nav>
    <div class="spacer"></div>
    <button class="btn ghost small" @click="S.rpOpen = !S.rpOpen">
      <span>{{ S.rpOpen ? '隐藏右栏' : '显示右栏' }}</span>
    </button>
    <button class="thbtn" :title="light ? '切换到深色' : '切换到浅色'" @click="toggleTheme">
      <svg class="i-sun" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><circle cx="12" cy="12" r="4.2" /><path d="M12 2.6v2.2M12 19.2v2.2M2.6 12h2.2M19.2 12h2.2M5.4 5.4l1.6 1.6M17 17l1.6 1.6M18.6 5.4L17 7M7 17l-1.6 1.6" /></svg>
      <svg class="i-moon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M20.5 14.4A8.6 8.6 0 1 1 9.6 3.5a6.9 6.9 0 0 0 10.9 10.9z" /></svg>
    </button>
    <div class="rolebar">
      <button
        v-for="id in ROLE_IDS"
        :key="id"
        class="rolebtn"
        :class="{ on: S.role === id }"
        @click="setRole(id)"
      >{{ id }}</button>
      <button class="rolebtn x" @click="S.view = 'workers'">
        <span v-html="icon('robot', 12)" /> Agent 工厂 {{ S.agents.filter((a) => a.product).length }}
      </button>
    </div>
    <div class="avatar">{{ (S.roleName || 'BA').slice(0, 2) }}</div>
  </div>

  <div class="body">
    <SideBar />
    <main class="stage">
      <component :is="current" />
    </main>
    <RightPane v-show="S.rpOpen" />
  </div>

  <AddProjectModal />

  <div id="toast">
    <div v-for="t in S.toasts" :key="t.id" class="tst" :class="t.kind">{{ t.text }}</div>
  </div>
</template>
