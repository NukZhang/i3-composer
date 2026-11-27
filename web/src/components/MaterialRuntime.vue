<script setup>
import { computed, ref } from 'vue';
import { api } from '../api.js';
import { S } from '../store.js';

const head = computed(() => S.state?.pbc === S.pbcId ? S.state?.commits?.[0] : null);
const ready = computed(() => {
  const test = S.state?.test;
  return !!head.value?.digest && test?.commit === head.value.no && test?.total > 0 && test.pass === test.total;
});
const canStart = computed(() => S.acts.includes('acceptAT'));
const started = ref(null);
const active = computed(() => started.value?.id === S.pbcId && started.value?.commit === head.value?.no ? started.value : null);
const busy = ref(false);
const error = ref('');

async function start() {
  const id = S.pbcId;
  busy.value = true;
  error.value = '';
  try {
    started.value = { ...(await api.materialPreview(id)), id };
  } catch (err) {
    error.value = err.message;
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <section class="card">
    <div class="ch">
      <h3>材料入库 · 业务制品</h3>
      <span class="spacer" />
      <button class="btn small primary" :disabled="!ready || !canStart || busy" @click="start">
        {{ busy ? '正在启动' : '启动材料入库验收' }}
      </button>
    </div>
    <p v-if="error" role="alert" style="color: var(--err)">{{ error }}</p>
    <p v-else-if="!ready" class="small muted">当前提交完成测试和构建后可启动业务页面。</p>
    <p v-else-if="!active" class="small muted">请由 BA 启动当前提交的测试实例。测试数据与 SIT 分开保存。</p>
    <template v-if="active">
      <div class="row wrap" style="gap: 8px; margin-bottom: 8px">
        <span class="small muted">测试实例 · 提交 {{ active.commit }} · {{ active.digest.slice(0, 19) }}…</span>
        <a class="btn small" :href="active.url" target="_blank" rel="noopener">在新窗口打开材料入库</a>
      </div>
      <iframe :src="active.url" title="材料入库业务验收" class="runtime-frame" />
    </template>
  </section>
</template>

<style scoped>
.runtime-frame {
  background: #fff;
  border: 1px solid var(--line-e);
  border-radius: 6px;
  width: 100%;
  min-height: 620px;
}
</style>
