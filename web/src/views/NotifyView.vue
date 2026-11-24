<script setup>
import PageHeader from '../components/PageHeader.vue';
import { S, unitById } from '../store.js';
import { icon } from '../icons.js';

function unitName(id) {
  const u = unitById(id);
  return u ? u.name : id;
}
function go(n) {
  S.pbcId = n.unit === 'agents' ? S.pbcId : n.unit || S.pbcId;
  S.view = n.unit === 'agents' ? 'workers' : 'plan';
}
</script>

<template>
  <PageHeader title="通知中心" sub="待确认 / 构建结果 / 部署结果 · 企微失败降级站内 · 全部来自账本事实" />
  <div v-if="!S.notifications.length" class="card"><div class="empty">暂无通知</div></div>
  <div v-else class="card">
    <div v-for="(n, i) in S.notifications" :key="i" class="row" style="padding: 8px 0; border-bottom: 1px solid var(--line-e); cursor: pointer" @click="go(n)">
      <span style="width: 6px; height: 6px; border-radius: 50%; background: var(--accent-deep)" />
      <span class="tag t-warn">{{ n.type }}</span>
      <span style="font-size: 12.5px">{{ n.text }}</span>
      <span class="spacer" style="flex: 1" />
      <span class="small muted">{{ n.at }}</span>
    </div>
    <div class="small muted" style="margin-top: 8px">
      通知不是另一张表：它由账本里的事件事实（发布 / 受阻 / tag / 部署 / 评测 / 放量）派生，删掉再拉一次也一样。
      单元：{{ S.notifications.map((n) => unitName(n.unit)).filter((v, i, a) => a.indexOf(v) === i).join('、') }}。
    </div>
  </div>
</template>
