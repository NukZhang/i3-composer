<script setup>
import { computed, ref, watch } from 'vue';
import { S, refresh, toast } from '../store.js';
import { api } from '../api.js';
import { icon } from '../icons.js';

// 「＋」打开的弹窗：新建（项目 / 工作空间）+ 加入已有（项目 / 工作空间）。
// 和原型 v2.7 §二 一致：两个 tab，覆盖「新建」和「已有」。

const name = ref('');
const ws = ref('');
const start = ref('blank');
const use = ref('org');
const busy = ref(false);

watch(
  () => S.addOpen,
  (v) => {
    if (v) {
      name.value = '';
      ws.value = '';
      start.value = 'blank';
      use.value = 'org';
      busy.value = false;
    }
  },
);

const wsOptions = computed(() => S.workspaces);
const catalogProjects = computed(() => S.catalog.projects || []);
const catalogSpaces = computed(() => S.catalog.spaces || []);
const joinedProjects = computed(() => new Set(S.projects.map((p) => p.id)));
const joinedSpaces = computed(() => new Set(S.workspaces.map((w) => w.name)));

function close() {
  S.addOpen = false;
}
function tab(t) {
  S.addTab = t;
}
function kind(k) {
  S.addKind = k;
}

async function submitNew() {
  const n = (name.value || '').trim();
  if (!n) {
    toast('给' + (S.addKind === 'proj' ? '项目' : '工作空间') + '起个名字', 'err');
    return;
  }
  busy.value = true;
  try {
    if (S.addKind === 'ws') {
      await api.createWorkspace({ name: n, use: use.value });
      toast('工作空间已建：「' + n + '」', 'ok');
    } else {
      const j = await api.createProject({ name: n, ws: ws.value || '', start: start.value });
      toast('项目已建：「' + n + '」', 'ok');
      S.projId = j.id;
      S.wsId = ws.value || '';
      S.pbcId = '';
      S.view = 'projects';
    }
    close();
    await refresh();
  } catch (e) {
    toast(e.message, 'err');
  } finally {
    busy.value = false;
  }
}

async function joinP(id) {
  busy.value = true;
  try {
    const j = await api.joinProject(id);
    toast('已加入：' + j.name + '（原归属不变）', 'ok');
    S.projId = j.id;
    S.pbcId = '';
    S.view = 'projects';
    close();
    await refresh();
  } catch (e) {
    toast(e.message, 'err');
  } finally {
    busy.value = false;
  }
}

async function joinW(id) {
  busy.value = true;
  try {
    const j = await api.joinWorkspace(id);
    toast('已加入工作空间：' + j.name, 'ok');
    close();
    await refresh();
  } catch (e) {
    toast(e.message, 'err');
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <div v-if="S.addOpen" id="modal" class="on" @click.self="close">
    <div class="mbox">
      <h2>新建项目 / 工作空间</h2>
      <div class="msub">项目是承接容器（一份 BRD + 多个交付单元）；工作空间是文件夹，把多个项目放在一起。两者都能现建，也能加入已有的。</div>
      <div class="row" style="gap: 6px; margin: 12px 0 14px">
        <button class="btn small" :class="{ primary: S.addTab === 'new' }" @click="tab('new')">
          <span v-html="icon('plus', 11)" /> 新建
        </button>
        <button class="btn small" :class="{ primary: S.addTab === 'join' }" @click="tab('join')">
          <span v-html="icon('layers', 11)" /> 加入已有
        </button>
      </div>

      <template v-if="S.addTab === 'new'">
        <div class="field">
          <label>建什么</label>
          <div class="row" style="gap: 6px">
            <button class="btn small" :class="{ primary: S.addKind === 'proj' }" @click="kind('proj')">项目</button>
            <button class="btn small" :class="{ primary: S.addKind === 'ws' }" @click="kind('ws')">工作空间</button>
          </div>
        </div>

        <template v-if="S.addKind === 'proj'">
          <div class="field"><label>项目名称</label><input v-model="name" placeholder="例如：门禁与访客改造" @keyup.enter="submitNew" /></div>
          <div class="field">
            <label>归入工作空间</label>
            <select v-model="ws">
              <option value="">不归入（独立项目）</option>
              <option v-for="w in wsOptions" :key="w.id" :value="w.id">{{ w.name }}</option>
            </select>
          </div>
          <div class="field">
            <label>起步方式</label>
            <select v-model="start">
              <option value="blank">空白项目（先建，BRD 后面写）</option>
              <option value="brd">先写 BRD（建完直接打开编辑器）</option>
            </select>
          </div>
          <div class="mfoot">
            <button class="btn" @click="close">取消</button>
            <button class="btn primary" :disabled="busy" @click="submitNew">创建项目</button>
          </div>
        </template>

        <template v-else>
          <div class="field"><label>工作空间名称</label><input v-model="name" placeholder="例如：星河湾" @keyup.enter="submitNew" /></div>
          <div class="field">
            <label>用途</label>
            <select v-model="use">
              <option value="org">按客户 / 园区归集</option>
              <option value="team">按团队归集</option>
            </select>
          </div>
          <div class="msub" style="margin-top: 0">工作空间只做归集，不产生制品：BRD 和交付单元永远挂在项目上。</div>
          <div class="mfoot">
            <button class="btn" @click="close">取消</button>
            <button class="btn primary" :disabled="busy" @click="submitNew">创建工作空间</button>
          </div>
        </template>
      </template>

      <template v-else>
        <div class="msub" style="margin: 0 0 10px">加入不等于复制：项目还在原处、还是原来的人在管，只是出现在你的侧栏。</div>
        <div class="small" style="color: var(--tx-4); margin-bottom: 4px">可加入的项目</div>
        <div v-for="c in catalogProjects" :key="c.id" class="row" style="padding: 7px 0; border-bottom: 1px solid var(--line-e); gap: 8px; align-items: flex-start">
          <div style="flex: 1; min-width: 0">
            <div class="small"><b>{{ c.name }}</b></div>
            <div class="small muted">{{ c.ws }} · {{ c.owner }} · {{ c.members }} 人 · {{ c.note }}</div>
          </div>
          <span v-if="joinedProjects.has(c.id)" class="tag t-ok" style="font-size: 9px">已在侧栏</span>
          <button v-else class="btn small" :disabled="busy" @click="joinP(c.id)">加入</button>
        </div>
        <div class="small" style="color: var(--tx-4); margin: 12px 0 4px">可加入的工作空间</div>
        <div v-for="c in catalogSpaces" :key="c.id" class="row" style="padding: 7px 0; border-bottom: 1px solid var(--line-e); gap: 8px; align-items: flex-start">
          <div style="flex: 1; min-width: 0">
            <div class="small"><b>{{ c.name }}</b></div>
            <div class="small muted">{{ c.projects }} 个项目 · {{ c.note }}</div>
          </div>
          <span v-if="joinedSpaces.has(c.name)" class="tag t-ok" style="font-size: 9px">已加入</span>
          <button v-else class="btn small" :disabled="busy" @click="joinW(c.id)">加入</button>
        </div>
        <div class="mfoot"><button class="btn" @click="close">关闭</button></div>
      </template>
    </div>
  </div>
</template>
