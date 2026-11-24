<script setup>
// AcceptTerminal —— 验收台里的「真机操作」。
//
// PBC：像用户一样点（筛账期、勾选、批量生成），每一步都记进复现步骤。
// Agent：对话验收——「你来当那个客户」，随便打一句，看它是不是还站得住。
//
// 这里的操作是演示界面，不写账本；真正的结论由「通过 / 不提过」那一步落事实。
import { computed, ref } from 'vue';
import { S, toast, refresh } from '../store.js';
import { api } from '../api.js';
import { icon } from '../icons.js';

const props = defineProps({
  kind: { type: String, default: 'pbc' },
  unit: { type: String, default: 'fee' },
  agent: { type: Object, default: null },
  chats: { type: Array, default: () => [] },
});

// ---------- PBC：缴费单列表（可点） ----------

const PERIODS = ['2026-10', '2026-11'];
const HOUSES = [
  { no: 'A 栋 101', name: '张女士', amount: 1280, overdue: 3 },
  { no: 'A 栋 102', name: '李先生', amount: 4600, overdue: 32 },
  { no: 'A 栋 201', name: '赵先生', amount: 86000, overdue: 21 },
  { no: 'B 栋 301', name: '陈女士', amount: 920, overdue: 8 },
  { no: 'B 栋 302', name: '周先生', amount: 3100, overdue: 45 },
  { no: 'C 栋 101', name: '孙女士', amount: 2400, overdue: 5 },
];

const period = ref(PERIODS[0]);
const picked = ref([]);
const steps = ref([]);
const generated = ref(0);

const rows = computed(() => HOUSES.filter((h) => h.overdue > 0));

function toggle(no) {
  const i = picked.value.indexOf(no);
  if (i >= 0) picked.value.splice(i, 1);
  else picked.value.push(no);
  note(`勾选 ${no}`);
}
function note(t) {
  steps.value.push(t);
}
function doFilter() {
  note(`筛选账期 ${period.value}`);
  // 原型里的那一处不自洽：筛完账期，「批量生成」按钮就消失了。
  if (period.value === PERIODS[1]) {
    steps.value.push('（按钮在筛选后消失了）');
  }
}
function doGenerate() {
  if (period.value === PERIODS[1]) {
    toast('按钮在筛选后消失了，无法二次确认', 'err');
    return;
  }
  generated.value = picked.value.length;
  note(`点批量生成（${picked.value.length} 户）`);
  toast(`已生成 ${picked.value.length} 份缴费单`, 'ok');
}

// ---------- Agent：对话验收 ----------

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

// 复现步骤：PBC 是操作记录，Agent 是对话记录。
const repro = computed(() => {
  if (props.kind === 'agent') {
    return chatList.value.map((c) => (c.who === 'me' ? '客户：' : 'Agent：') + c.t).join('\n');
  }
  return steps.value.length ? steps.value.join(' → ') : '';
});
defineExpose({ repro });
</script>

<template>
  <div class="card" style="margin: 0; padding: 0; overflow: hidden">
    <!-- PBC：真机操作 -->
    <template v-if="kind !== 'agent'">
      <div class="ch" style="padding: 8px 11px; border-bottom: 1px solid var(--line-e)">
        <span v-html="icon('cloud')" />
        <h3 style="font-size: 12px">真机操作 · test 环境</h3>
        <span class="spacer" />
        <span class="small muted">下面直接点，像用户一样用</span>
      </div>
      <div style="padding: 10px 12px; background: #fff; color: #1f2430; min-height: 274px">
        <div class="row" style="gap: 7px; align-items: center; margin-bottom: 9px">
          <select v-model="period" class="btn small" style="padding: 4px 8px">
            <option v-for="p in PERIODS" :key="p" :value="p">账期 {{ p }}</option>
          </select>
          <button class="btn small" @click="doFilter">筛选</button>
          <button class="btn small primary" :disabled="period === PERIODS[1]" @click="doGenerate">
            批量生成缴费单
          </button>
          <span class="small" style="color: #8a93a3">已选 {{ picked.length }} 户 · 已生成 {{ generated }} 份</span>
        </div>
        <table style="width: 100%; border-collapse: collapse; font-size: 12px">
          <thead>
            <tr>
              <th style="text-align: left; padding: 4px 6px; color: #8a93a3; font-weight: 500">房号</th>
              <th style="text-align: left; padding: 4px 6px; color: #8a93a3; font-weight: 500">客户</th>
              <th style="text-align: left; padding: 4px 6px; color: #8a93a3; font-weight: 500">金额</th>
              <th style="text-align: left; padding: 4px 6px; color: #8a93a3; font-weight: 500">逾期</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="h in rows" :key="h.no" style="border-bottom: 1px solid #f2f4f7">
              <td style="padding: 5px 6px">
                <input type="checkbox" :checked="picked.includes(h.no)" @change="toggle(h.no)" />
                <span style="margin-left: 5px">{{ h.no }}</span>
              </td>
              <td style="padding: 5px 6px">{{ h.name }}</td>
              <td style="padding: 5px 6px">¥{{ h.amount.toLocaleString() }}</td>
              <td style="padding: 5px 6px">{{ h.overdue }} 天</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div class="ac-steps">
        操作记录：{{ steps.length ? steps.join(' → ') : '打开列表 → 勾选一栋楼 → 点批量生成' }}
      </div>
    </template>

    <!-- Agent：对话验收 -->
    <template v-else>
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
    </template>
  </div>
</template>
