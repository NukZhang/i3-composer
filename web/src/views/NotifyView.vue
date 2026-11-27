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
  <!-- 说实际情形：外部 IM 通道没接，不存在「先发企微、发不出去才落站内」这条降级路径。
       以前写「企微失败降级站内」等于编了一个不存在的架构（宪法 II），与 TechView 的「未接」列表也矛盾。 -->
  <PageHeader title="通知中心" sub="查看账本中的受阻、打 tag、部署与 Agent 发布记录。通知只到站内。" />
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
