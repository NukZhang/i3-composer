<script setup>
import { computed } from 'vue';
import { S } from '../store.js';
import { icon } from '../icons.js';

// 运行时面板：显示的是真实的事实流（/api/facts），不是模拟日志。
// 「接口与状态」列的是本页写的接口契约——它帮助评审者知道这一页真在做什么。
const IMPL = {
  plan: [
    ['提交', 'POST /api/artifacts {artifact,text|files}'],
    ['状态', 'draft → accepted（提交时）'],
    ['事实', 'artifact.drafted / commit.created'],
    ['闸', '引用闭合 · 全 accepted · 镜像=树 · 原型还原'],
  ],
  proto: [
    ['批注', 'POST /api/annotations {x,y,w,h,text}'],
    ['坐标', '归一化 0~1，可定位'],
    ['事实', 'annotation.added → 随提交落库'],
  ],
  build: [
    ['草稿', 'POST /api/artifacts {artifact:"code",files}'],
    ['执行', 'POST /api/items/{id}/run'],
    ['事实', 'item.step.done / build.succeeded / deploy.succeeded'],
  ],
  verify: [
    ['门禁', 'GET /api/state → gates'],
    ['评测', 'POST /api/agents/{id}/eval'],
    ['验收', 'POST /api/units/{id}/svcverify'],
  ],
  release: [
    ['发布', 'POST /api/agents/{id}/publish（SA）'],
    ['放量', 'POST /api/agents/{id}/rollout {pct:10|50|100}'],
    ['回滚', 'POST /api/agents/{id}/rollback'],
  ],
  agent: [
    ['定义', 'GET /api/agents/{id}'],
    ['评测', 'POST /api/agents/{id}/eval'],
    ['基线', 'POST /api/agents/{id}/baseline {runId}'],
  ],
};

const rows = computed(() => IMPL[S.view] || [['说明', '切换到方案 / 原型 / 构建 / 验证 / 发布查看对应接口契约']]);
const rt = computed(() => S.runtime);
</script>

<template>
  <aside class="rpane" id="rpane">
    <div class="rp-head">
      <div class="rp-tab" :class="{ on: S.rpTab === 'rt' }" @click="S.rpTab = 'rt'">运行时</div>
      <div class="rp-tab" :class="{ on: S.rpTab === 'impl' }" @click="S.rpTab = 'impl'">接口与状态</div>
    </div>
    <div class="rp-body">
      <template v-if="S.rpTab === 'rt'">
        <div v-if="!rt.length" class="small muted" style="padding: 8px">等待运行时事件…</div>
        <div v-for="(r, i) in rt" :key="i" class="rt-line">
          <span class="rt-t">{{ r.at }}</span>
          <span class="rt-k" :class="'k-' + r.k">{{ r.k }}</span>
          <span class="rt-x">{{ r.x }}</span>
        </div>
      </template>
      <template v-else>
        <div v-for="(r, i) in rows" :key="i" class="impl-row">
          <div class="k">{{ r[0] }}</div>
          <div class="v">{{ r[1] }}</div>
        </div>
      </template>
    </div>
  </aside>
</template>
