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
    ['编排', 'PUT /api/agents/{id}/flow {flow}'],
    ['构建', 'POST /api/agents/{id}/build（换 digest 与版本号）'],
    ['试跑', 'POST /api/agents/{id}/trial'],
    ['用例', 'POST /api/agents/{id}/cases（沉淀为评测用例）'],
    ['草稿', 'POST /api/artifacts {artifact:"code",files}'],
    ['执行', 'POST /api/items/{id}/run'],
  ],
  verify: [
    ['判定', 'POST /api/checks/{id}/decide {st,title,sev,steps}'],
    ['对话', 'POST /api/units/{id}/chat {text}'],
    ['门禁', 'GET /api/state → gates'],
    ['评测', 'POST /api/agents/{id}/eval'],
  ],
  release: [
    ['发布', 'POST /api/agents/{id}/publish（SA）'],
    ['放量', 'POST /api/agents/{id}/rollout {pct:10|50|100}'],
    ['回滚', 'POST /api/agents/{id}/rollback'],
  ],
  agent: [
    ['定义', 'GET /api/agents/{id}'],
    ['编排', 'GET /api/agents/{id} → flow / kb'],
    ['评测', 'POST /api/agents/{id}/eval'],
    ['基线', 'POST /api/agents/{id}/baseline {runId}'],
    ['问题单', 'POST /api/agents/{id}/kb/{idx}/dispatch'],
  ],
};

const rows = computed(() => IMPL[S.view] || [['说明', '切换到方案 / 原型 / 构建 / 验证 / 发布查看对应接口契约']]);
const rt = computed(() => S.runtime);
// events 是经 pub/sub 收到的事件：发出去 → 收回来 → 落成事实。
const events = computed(() => (S.state && S.state.events) || []);
</script>

<template>
  <aside class="rpane" id="rpane">
    <div class="rp-head">
      <div class="rp-tab" :class="{ on: S.rpTab === 'rt' }" @click="S.rpTab = 'rt'">运行时</div>
      <div class="rp-tab" :class="{ on: S.rpTab === 'impl' }" @click="S.rpTab = 'impl'">接口与状态</div>
    </div>
    <div class="rp-body">
      <template v-if="S.rpTab === 'rt'">
        <div v-if="events.length" style="padding: 8px 8px 4px">
          <div class="small muted" style="margin-bottom: 4px">经 pub/sub 收到的事件（{{ events.length }}）</div>
          <div v-for="(e, i) in events" :key="'ev' + i" class="rt-line">
            <span class="rt-t">{{ e.at }}</span>
            <span class="rt-k k-ACTOR">{{ e.topic }}</span>
            <span class="rt-x">{{ e.eventId || (e.data && e.data.tag) || '' }}</span>
          </div>
        </div>
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
