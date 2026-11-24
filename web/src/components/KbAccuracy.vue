<script setup>
// KbAccuracy —— 知识库准确度（原型 v2.4 / v2.6 的第二条轴）。
//
// 回答错了要先分清是没查到还是答歪了：召回差 → 补文档、调 topK、重排，
// 是知识库的活；答歪了 → 改提示词、加约束，是 Agent 的活。
// 混成一句「Agent 不准」，最后谁都改不动。
import { computed } from 'vue';
import { toast, refresh } from '../store.js';
import { api } from '../api.js';
import { icon } from '../icons.js';

const props = defineProps({
  agent: { type: Object, default: null },
  kb: { type: Object, default: null },
});

const failTotal = computed(() => {
  const a = props.agent;
  if (!a || !a.eval) return 0;
  return a.eval[1] - a.eval[0];
});

function cell(k, v, sub, col) {
  return { k, v, sub, col };
}

const metrics = computed(() => {
  const kb = props.kb;
  if (!kb) return [];
  return [
    cell('召回率 Recall@5', (kb.recall * 100).toFixed(1) + '%', '该查到的查到了', kb.recall >= 0.9 ? 'var(--ok)' : 'var(--warn)'),
    cell('首条命中 MRR', kb.mrr.toFixed(2), '答案排第一', kb.mrr >= 0.85 ? 'var(--ok)' : 'var(--warn)'),
    cell('忠实度', (kb.faithful * 100).toFixed(1) + '%', '有出处支撑的回答占比', kb.faithful >= 0.9 ? 'var(--ok)' : 'var(--warn)'),
    cell('引用命中率', (kb.cite * 100).toFixed(1) + '%', '引用真的在那段里', kb.cite >= 0.9 ? 'var(--ok)' : 'var(--ok)'),
  ];
});

async function dispatch(idx) {
  if (!props.agent) return;
  try {
    await api.kbDispatch(props.agent.id, idx);
    toast('已派给知识库 owner', 'ok');
  } catch (e) {
    toast(e.message, 'err');
  }
  await refresh();
}
</script>

<template>
  <div v-if="!kb" class="card"><div class="empty">这个 Agent 不依赖外部知识库。</div></div>
  <div v-else class="card">
    <div class="ch"><span v-html="icon('doc')" /><h3>知识库准确度</h3><span class="spacer" />
      <span class="small muted">回答错了，先分清是没查到还是答歪了</span>
    </div>
    <div class="ev-metrics" style="grid-template-columns: repeat(4, 1fr)">
      <div v-for="m in metrics" :key="m.k" class="ev-m">
        <div class="ev-k">{{ m.k }}</div>
        <div class="ev-v" :style="{ color: m.col }">{{ m.v }}</div>
        <div class="ev-s">{{ m.sub }}</div>
      </div>
    </div>

    <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 9px">
      <div class="card tight" style="margin: 0; background: var(--bg-panel)">
        <div class="small muted">失败归因 · {{ failTotal }} 条不过</div>
        <div style="font-size: 13px; line-height: 2; margin-top: 3px">
          <span style="color: var(--warn)">检索没召回</span> <b>{{ kb.attr.notFound }}</b> 条
          <span class="small muted">（补文档 / 调 topK）</span><br />
          <span style="color: var(--err)">检索到了但答歪</span> <b>{{ kb.attr.wrongGen }}</b> 条
          <span class="small muted">（改提示词 / 加约束）</span>
        </div>
      </div>
      <div class="card tight" style="margin: 0; background: var(--bg-panel)">
        <div class="small muted">知识库问题单</div>
        <div style="font-size: 13px; line-height: 2; margin-top: 3px">
          <b>{{ (kb.issues || []).reduce((a, x) => a + x.c, 0) }}</b> 条待修 ·
          <span class="small muted">缺文档 / 过期 / 两份文档打架</span>
        </div>
        <div class="small muted" style="margin-top: 3px">派给库的 owner，不派给 Agent 的 owner</div>
      </div>
    </div>

    <div v-if="(kb.issues || []).length" style="margin-top: 10px">
      <div
        v-for="(x, i) in kb.issues"
        :key="i"
        class="row"
        style="padding: 7px 0; border-bottom: 1px solid var(--line-e); gap: 9px; align-items: flex-start"
      >
        <span class="tag t-warn" style="font-size: 9px; min-width: 26px; justify-content: center">{{ x.c }}</span>
        <div style="flex: 1">
          <div class="small" style="color: var(--tx-2)">{{ x.n }}</div>
          <div class="small muted">{{ x.ex }}</div>
        </div>
        <span v-if="x.dispatched" class="tag t-gray" style="font-size: 9px">已派</span>
        <button v-else class="btn small" @click="dispatch(i)">派给 owner</button>
      </div>
    </div>

    <div class="small muted" style="margin-top: 9px; line-height: 1.8">
      <b>这两类失败不能混着改。</b>召回差 → 补文档、调 topK、做重排，是知识库的活；
      答歪了 → 改提示词、加「没依据就说不知道」的约束，是 Agent 的活。
      另外：知识库版本变了要重跑评测。库更新了但评测没重跑，等于拿旧分数给新系统背书。
    </div>
  </div>
</template>
