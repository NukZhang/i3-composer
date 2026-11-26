<script setup>
import { computed, ref, watch } from 'vue';
import PageHeader from '../components/PageHeader.vue';
import { S, refresh, toast, currentProject, currentWorkspace } from '../store.js';
import { api } from '../api.js';
import { icon } from '../icons.js';

const brd = computed(() => S.brd || { v: '—', versions: [], text: '', words: 0 });
const proj = computed(() => currentProject());
const ws = computed(() => currentWorkspace());
const title = computed(() => {
  const p = proj.value;
  if (!p) return 'BRD';
  return 'BRD · ' + (ws.value ? ws.value.name + ' / ' : '') + p.name;
});
const can = computed(() => (S.acts || []).includes('brd'));
const text = ref('');
const note = ref('');
const dirty = ref(false);

watch(
  () => brd.value.v,
  () => {
    text.value = brd.value.text || '';
    dirty.value = false;
  },
  { immediate: true },
);

function onInput(e) {
  text.value = e.target.value;
  dirty.value = true;
}

async function save() {
  if (!can.value) {
    toast('只有 BA 能保存 BRD', 'err');
    return;
  }
  if (!text.value.trim()) {
    toast('内容为空，不能保存', 'err');
    return;
  }
  try {
    const j = await api.saveBRD(text.value, note.value || '人工编辑：' + text.value.split('\n')[0].slice(0, 18), S.projId);
    toast('已保存草稿 ' + j.v, 'ok');
    note.value = '';
  } catch (e) {
    toast(e.message, 'err');
  }
  await refresh();
}
async function suggest() {
  // 「续写」是建议，不是自动改写：把建议追加进草稿区，由 BA 决定要不要保存。
  // 内容是通用骨架，不塞别的项目的业务——新项目不该继承悦府的规则。
  const name = (proj.value && proj.value.name) || '本项目';
  const add = [
    '',
    '### 待确认事项（X-业务分析工程师整理）',
    '- ' + name + '：谁在什么场景下用、现在怎么解决',
    '- 做完之后哪个数会变（量化口径）',
    '- 不做的话哪个数不会变',
    '- 边界与例外：哪些情况不做 / 要人工确认',
    '',
  ].join('\n');
  text.value = text.value.replace(/\n## 4\. 关键规则[\s\S]*$/, '') + '\n## 4. 关键规则\n' + add;
  dirty.value = true;
  toast('X-业务分析工程师已续写一版「待确认事项」（未保存），请 BA 检查后保存', 'warn');
}
</script>

<template>
  <PageHeader
    :title="title"
    :sub="'项目级制品。渐进编写：每次保存生成草稿版本，走同一套提交模型；X-业务分析工程师可续写，BA 确认。' + (proj && !proj.brd ? ' 这一份还没建过：先保存一次就是 v0.1。' : '')"
  >
    <button class="btn small" @click="S.view = 'projects'"><span v-html="icon('box', 12)" /> 返回项目</button>
    <button class="btn small" @click="suggest"><span v-html="icon('robot', 12)" /> 让 X 续写下一节</button>
    <button class="btn small primary" :disabled="!can" @click="save">保存为草稿</button>
  </PageHeader>

  <div v-if="!can" class="perm-note">
    <span v-html="icon('lock', 12)" /> 当前角色 <b>{{ S.roleName || S.role }}</b> 无权编辑 BRD（BA 专属）
  </div>

  <div class="card" style="padding: 0; overflow: hidden; margin-bottom: 11px">
    <div class="ed-bar">
      <span class="mono small" style="color: var(--info)">BRD.md</span>
      <span class="spacer" style="flex: 1" />
      <span class="small muted">{{ text.length }} 字 · {{ text.split('\n').length }} 行</span>
      <span v-if="dirty" class="tag t-warn" style="margin-left: 8px">未保存</span>
      <input v-model="note" placeholder="保存说明（可选）" style="width: 200px; margin-left: 8px; padding: 4px 8px; font-size: 11px" />
    </div>
    <textarea id="brd-ed" class="md-edit" spellcheck="false" :readonly="!can" :value="text" @input="onInput" />
  </div>

  <div class="card">
    <div class="ch">
      <span v-html="icon('git')" /><h3>BRD 版本线（渐进）</h3><span class="spacer" />
      <span class="small muted">{{ brd.v }} · 草稿随提交进入制品树</span>
    </div>
    <div v-for="v in (brd.versions || []).slice().reverse()" :key="v.v + v.at" class="row" style="padding: 5px 0; border-bottom: 1px solid var(--line-e); font-size: 11.5px">
      <span class="mono" style="color: var(--warn); width: 50px">{{ v.v }}</span>
      <span class="tag" :class="v.st === 'draft' ? 't-warn' : v.st === 'superseded' ? 't-gray' : 't-ok'" style="font-size: 9px">
        {{ v.st === 'draft' ? '草稿' : v.st === 'superseded' ? '已被覆盖' : '已接受' }}
      </span>
      <span class="muted" style="flex: 1">{{ v.note }}</span>
      <span class="small muted">{{ v.by }} · {{ v.at }}</span>
    </div>
    <div v-if="!(brd.versions || []).length" class="small muted">还没有版本。保存一次就会生成 v1.0 草稿。</div>
  </div>
</template>
