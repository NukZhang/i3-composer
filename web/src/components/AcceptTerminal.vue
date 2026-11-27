<script setup>
// Agent 验收对话和缺陷复现用同一份记录。
import { computed, ref } from 'vue';
import { toast, refresh } from '../store.js';
import { api } from '../api.js';
import { icon } from '../icons.js';

const props = defineProps({
  unit: { type: String, required: true },
  agent: { type: Object, default: null },
  chats: { type: Array, default: () => [] },
});

const input = ref('');
const sending = ref(false);

const chatList = computed(() => {
  const seed = props.chats && props.chats.length
    ? props.chats
    : [{ who: 'me', t: '你们这个热玛吉能维持多久？' }];
  return seed;
});

async function send() {
  const t = input.value.trim();
  if (!t) return;
  if (!props.agent) {
    toast('这个单元没有关联 Agent，不能对话验收', 'warn');
    return;
  }
  sending.value = true;
  input.value = '';
  try {
    await api.verifyChat(props.unit, t);
  } catch (e) {
    toast(e.message, 'err');
    input.value = t;
  }
  sending.value = false;
  await refresh();
}

const repro = computed(() =>
  chatList.value.map((c) => (c.who === 'me' ? '客户：' : 'Agent：') + c.t).join('\n'),
);
defineExpose({ repro });
</script>

<template>
  <div class="card" style="margin: 0; padding: 0; overflow: hidden">
    <div class="ch" style="padding: 8px 11px; border-bottom: 1px solid var(--line-e)">
      <span v-html="icon('robot')" />
      <h3 style="font-size: 12px">试一遍 · 你来当那个客户</h3>
      <span class="spacer" />
      <span class="small muted">真的发一句，看它怎么答</span>
    </div>
    <div class="ac-term">
      <div v-for="(c, i) in chatList" :key="i" class="m" :class="{ me: c.who === 'me' }">
        <div class="who">{{ c.who === 'me' ? '客户' : (agent ? agent.n : 'Agent') }}</div>
        <div class="bubble">{{ c.t }}</div>
      </div>
    </div>
    <div class="row" style="gap: 6px; padding: 8px 10px; border-top: 1px solid var(--line-e); background: var(--bg-panel)">
      <input
        v-model="input"
        placeholder="以客户身份说一句话，回车发送"
        style="flex: 1; font-size: 11.5px"
        @keydown.enter="send"
      />
      <button class="btn small primary" :disabled="sending" @click="send">发送</button>
    </div>
  </div>
</template>
