<script setup>
import { computed, ref } from 'vue';
import PageHeader from '../components/PageHeader.vue';
import ProtoAgentReview from '../components/ProtoAgentReview.vue';
import ProtoDunConsole from '../components/ProtoDunConsole.vue';
import { S, unitKind, currentAgent, toast, refresh } from '../store.js';
import { icon } from '../icons.js';
import { api } from '../api.js';
import { latest, staleList, kindCls, kindText } from '../derive.js';

const versions = computed(() => (S.state && S.state.versions) || {});
const kind = computed(() => unitKind());
const proto = computed(() => latest(versions.value, 'proto') || {});
const annos = computed(() => (S.state && S.state.annos) || []);
const agent = computed(() => currentAgent());
const stale = computed(() => staleList(versions.value).find((x) => x.t === 'proto'));

const annoOn = ref(false);
const rect = ref(null);
const start = ref(null);
const wrap = ref(null);
const annoText = ref('');
const showAnno = ref(false);

function down(e) {
  if (!annoOn.value) return;
  const r = wrap.value.getBoundingClientRect();
  start.value = {
    x: Math.max(0, Math.min(1, (e.clientX - r.left) / r.width)),
    y: Math.max(0, Math.min(1, (e.clientY - r.top) / r.height)),
  };
  rect.value = { x: start.value.x, y: start.value.y, w: 0, h: 0 };
  wrap.value.setPointerCapture(e.pointerId);
  e.preventDefault();
}
function move(e) {
  if (!start.value) return;
  const r = wrap.value.getBoundingClientRect();
  const x = Math.max(0, Math.min(1, (e.clientX - r.left) / r.width));
  const y = Math.max(0, Math.min(1, (e.clientY - r.top) / r.height));
  rect.value = {
    x: Math.min(x, start.value.x),
    y: Math.min(y, start.value.y),
    w: Math.abs(x - start.value.x),
    h: Math.abs(y - start.value.y),
  };
}
function up() {
  if (!start.value) return;
  start.value = null;
  if (!rect.value || rect.value.w < 0.02 || rect.value.h < 0.02) {
    rect.value = null;
    return;
  }
  showAnno.value = true;
}
async function saveAnno() {
  const t = annoText.value.trim();
  if (!t) {
    toast('请填写意见', 'err');
    return;
  }
  const r = rect.value || { x: 0.3, y: 0.3, w: 0.2, h: 0.1 };
  try {
    await api.addAnnotation({ x: r.x, y: r.y, w: r.w, h: r.h, text: t, round: '待提交' });
    toast('批注已记录为一条事实（annotation.added）', 'ok');
  } catch (e) {
    toast(e.message, 'err');
  }
  showAnno.value = false;
  annoText.value = '';
  rect.value = null;
  annoOn.value = false;
  await refresh();
}
</script>

<template>
  <PageHeader
    :title="'原型 <span class=&quot;mono&quot; style=&quot;color:var(--purple);font-size:13px&quot;>' + (proto.v || '—') + '</span>'"
    :sub="kind === 'agent'
      ? 'Agent 交付的是一屏工具：它的产出（判定 / 草稿）在这里被人看、被人改、被人拍板。就这一屏，没有别的。'
      : kind === 'hybrid'
        ? '组合项交付的是一套业务系统：策略、任务、话术三块，Agent 只负责第三块的话术生成。'
        : '线框与批注都来自账本：批注是 annotation.added 事实，坐标归一化存储，可定位。'"
  >
    <button v-if="kind === 'pbc'" class="btn small" :class="{ primary: annoOn }" @click="annoOn = !annoOn">
      <span v-html="icon('layout', 12)" /> {{ annoOn ? '框选中…（点此退出）' : '框选批注' }}
    </button>
    <button class="btn small" @click="S.view = 'plan'"><span v-html="icon('doc', 12)" /> 回方案页</button>
  </PageHeader>

  <div class="unitbar">
    <span class="tag" :class="kindCls(kind)" style="font-size: 9px">{{ kindText(kind) }}</span>
    <span class="muted">制品原型 {{ proto.v || '—' }} · 衍生自 {{ (proto.df || []).map((d) => d.t.toUpperCase() + ' ' + d.v).join(' · ') || '—' }}</span>
    <span class="spacer" style="flex: 1" />
    <span v-if="stale" class="muted" style="color: var(--err)">上游已变，待重算</span>
  </div>

  <template v-if="kind === 'agent'">
    <ProtoAgentReview :agent="agent" />
    <div class="card">
      <div class="ch"><span v-html="icon('list')" /><h3>评审批注（{{ annos.length }}）</h3><span class="spacer" /><span class="small muted">坐标归一化，可定位</span></div>
      <div v-if="!annos.length" class="small muted">暂无批注。</div>
      <div v-for="(a, i) in annos" :key="i" class="anno-item">
        <div class="anno-idx">{{ i + 1 }}</div>
        <div style="flex: 1">
          {{ a.text }}
          <div class="small muted mono">x {{ a.x.toFixed(2) }} y {{ a.y.toFixed(2) }} w {{ a.w.toFixed(2) }} h {{ a.h.toFixed(2) }} · {{ a.by }}</div>
        </div>
      </div>
    </div>
    <div class="card">
      <div class="ch"><span v-html="icon('git')" /><h3>原型版本线</h3></div>
      <div v-for="x in (versions.proto || []).slice().reverse()" :key="x.v" class="row" style="padding: 4px 0; border-bottom: 1px solid var(--line-e); font-size: 11.5px">
        <span class="mono" style="color: var(--purple); width: 52px">{{ x.v }}</span>
        <span class="tag" :class="x.st === 'draft' ? 't-warn' : 't-ok'" style="font-size: 9px">{{ x.st === 'draft' ? '草稿' : '已接受' }}</span>
        <span class="muted" style="flex: 1">{{ x.note || '' }}</span>
        <span class="small muted">{{ x.by }} · {{ x.at }}</span>
      </div>
    </div>
  </template>

  <template v-else-if="kind === 'hybrid'">
    <ProtoDunConsole :agent="agent" />
    <div class="card">
      <div class="ch"><span v-html="icon('git')" /><h3>原型版本线</h3></div>
      <div v-for="x in (versions.proto || []).slice().reverse()" :key="x.v" class="row" style="padding: 4px 0; border-bottom: 1px solid var(--line-e); font-size: 11.5px">
        <span class="mono" style="color: var(--purple); width: 52px">{{ x.v }}</span>
        <span class="tag" :class="x.st === 'draft' ? 't-warn' : 't-ok'" style="font-size: 9px">{{ x.st === 'draft' ? '草稿' : '已接受' }}</span>
        <span class="muted" style="flex: 1">{{ x.note || '' }}</span>
        <span class="small muted">{{ x.by }} · {{ x.at }}</span>
      </div>
    </div>
  </template>

  <template v-else>
    <div class="frame" :class="{ anno: annoOn }" ref="wrap" @pointerdown="down" @pointermove="move" @pointerup="up">
      <div class="screens">
        <div v-for="(nm, i) in ['缴费单列表', '生成缴费单', '对账中心']" :key="nm" class="scr">
          <div class="cap">{{ nm }} · #{{ i + 1 }}</div>
          <div class="wb h" />
          <div class="wb" />
          <div v-if="i === 1" class="wbox" />
          <template v-else><div class="wb" /><div class="wb" /></template>
          <div class="wb a" />
        </div>
      </div>
      <div
        v-for="(a, i) in annos"
        :key="'ar' + i"
        class="ar"
        :style="'left:calc(' + a.x * 100 + '% + 12px);top:calc(' + a.y * 100 + '% + 12px);width:calc(' + a.w * 100 + '% - 4px);height:calc(' + a.h * 100 + '% - 4px)'"
      />
      <div
        v-for="(a, i) in annos"
        :key="'lb' + i"
        :style="'position:absolute;left:calc(' + a.x * 100 + '% + 14px);top:calc(' + a.y * 100 + '% + 10px);font-size:10px;background:var(--anno-bg);color:var(--anno-tx);border-radius:4px;padding:0 5px;transform:translateY(-100%);pointer-events:none'"
      >{{ i + 1 }}</div>
      <div
        v-if="rect"
        class="ar"
        :style="'left:calc(' + rect.x * 100 + '% + 12px);top:calc(' + rect.y * 100 + '% + 12px);width:calc(' + rect.w * 100 + '% - 4px);height:calc(' + rect.h * 100 + '% - 4px)'"
      />
    </div>
    <div class="small muted" style="margin: 7px 0 11px">
      {{ annoOn ? '在原型上拖拽框选区域，松开后填写意见；批注会产生一条事实，进工作区。' : '点「框选批注」后可在原型上拖框标注。' }}
    </div>

    <div v-if="showAnno" class="card" style="border-color: var(--line-purple)">
      <div class="ch"><span v-html="icon('plus')" /><h3>批注</h3></div>
      <div class="field">
        <label>意见</label>
        <textarea v-model="annoText" placeholder="例如：此处改为列表页，支持按账期筛选" />
      </div>
      <div class="row">
        <button class="btn primary small" @click="saveAnno">提交批注</button>
        <button class="btn small" @click="showAnno = false; rect = null; annoOn = false">取消</button>
      </div>
    </div>

    <div class="card">
      <div class="ch"><span v-html="icon('list')" /><h3>评审批注（{{ annos.length }}）</h3><span class="spacer" /><span class="small muted">坐标归一化，可定位</span></div>
      <div v-if="!annos.length" class="small muted">暂无批注。点右上「框选批注」可在原型上拖框标注。</div>
      <div v-for="(a, i) in annos" :key="i" class="anno-item">
        <div class="anno-idx">{{ i + 1 }}</div>
        <div style="flex: 1">
          {{ a.text }}
          <div class="small muted mono">x {{ a.x.toFixed(2) }} y {{ a.y.toFixed(2) }} w {{ a.w.toFixed(2) }} h {{ a.h.toFixed(2) }} · {{ a.by }} · {{ a.at }}</div>
        </div>
      </div>
    </div>

    <div class="card">
      <div class="ch"><span v-html="icon('git')" /><h3>原型版本线</h3></div>
      <div v-for="x in (versions.proto || []).slice().reverse()" :key="x.v" class="row" style="padding: 4px 0; border-bottom: 1px solid var(--line-e); font-size: 11.5px">
        <span class="mono" style="color: var(--purple); width: 52px">{{ x.v }}</span>
        <span class="tag" :class="x.st === 'draft' ? 't-warn' : 't-ok'" style="font-size: 9px">{{ x.st === 'draft' ? '草稿' : '已接受' }}</span>
        <span class="muted" style="flex: 1">{{ x.note || '' }}</span>
        <span class="small muted">{{ x.by }} · {{ x.at }}</span>
      </div>
    </div>
  </template>
</template>
