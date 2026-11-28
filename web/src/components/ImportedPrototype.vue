<script setup>
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue';
import PrototypeImportPreview from './PrototypeImportPreview.vue';
import PrototypeInteractivePreview from './PrototypeInteractivePreview.vue';
import { api, MAX_PROTOTYPE_IMPORT_BYTES } from '../api.js';
import { S, currentUnit, refresh, toast } from '../store.js';

const props = defineProps({
  unit: { type: String, required: true },
  imports: { type: Array, required: true },
  annotations: { type: Array, required: true },
  canWrite: { type: Boolean, required: true },
});

const history = computed(() => props.imports.slice().sort((a, b) => b.seq - a.seq));
const requestedSeq = ref(null);
const lastSeq = ref(null);
const selected = computed(() => history.value.find((item) => item.seq === requestedSeq.value)
  || history.value.find((item) => item.seq === lastSeq.value) || history.value[0]);
const lines = computed(() => props.annotations.filter((a) => a.importSeq === selected.value?.seq && a.line));
const fileInput = ref(null);
const drawButton = ref(null);
const overlay = ref(null);
const note = ref(null);
const previewReady = ref(false);
const uploading = ref(false);
const saving = ref(false);
const uploadError = ref('');
const saveError = ref('');
const interactive = ref(false);

const drawing = ref(false);
const draft = ref(null);
const text = ref('');
const cursor = ref(null);
let start = null;
let pointerId = null;
let selectionRevision = 0;
let manualSelectionRevision = 0;
let pickerTarget = null;
const lifetime = new AbortController();
onBeforeUnmount(() => lifetime.abort());

function resetDrawing() {
  if (pointerId !== null && overlay.value?.hasPointerCapture(pointerId)) overlay.value.releasePointerCapture(pointerId);
  drawing.value = false;
  draft.value = null;
  text.value = '';
  cursor.value = null;
  start = null;
  pointerId = null;
  saveError.value = '';
}

watch(() => selected.value?.seq, (seq) => {
  lastSeq.value = seq ?? null;
  selectionRevision++;
  previewReady.value = false;
  interactive.value = false;
  resetDrawing();
}, { immediate: true, flush: 'sync' });
watch(() => props.canWrite, (allowed) => {
  if (!allowed) resetDrawing();
});
watch(() => S.role, () => {
  interactive.value = false;
});

function updatePreviewStatus(seq, ready) {
  if (seq !== selected.value?.seq) return;
  previewReady.value = ready;
  if (!ready) resetDrawing();
}

function choose(event) {
  manualSelectionRevision++;
  requestedSeq.value = Number(event.target.value);
}

function stillInUnit(unit) {
  return !lifetime.signal.aborted && S.pbcId === unit;
}

function uploadTarget() {
  return { unit: props.unit, createdSeq: currentUnit()?.registrySeq };
}

function openPicker() {
  // 文件选择器打开期间，账本可能已作废并重新登记同名的单元。
  pickerTarget = uploadTarget();
  fileInput.value?.click();
}

async function upload(event) {
  const input = event.currentTarget;
  const file = input.files?.[0];
  input.value = '';
  const { unit, createdSeq } = pickerTarget || uploadTarget();
  pickerTarget = null;
  if (!file || uploading.value || !props.canWrite) return;

  const revision = manualSelectionRevision;
  uploadError.value = '';
  uploading.value = true;
  try {
    if (file.size > MAX_PROTOTYPE_IMPORT_BYTES) throw new Error('原型文件不能超过 20 MiB');
    const signature = await file.slice(0, 5).text();
    if (file.type === 'application/pdf' || /\.pdf$/i.test(file.name) || signature === '%PDF-') {
      try {
        const { renderPDFPage } = await import('../prototypePDF.js');
        await renderPDFPage(file, document.createElement('canvas'), lifetime.signal, { validate: true });
      } catch (e) {
        throw new Error('PDF 首页无法预览。' + e.message);
      }
    }
    if (!stillInUnit(unit) || !S.acts.includes('acceptProto')) return;
    const imported = await api.importPrototype(unit, file, createdSeq);
    // 等服务端投影读到该序号再预览；期间用户手动选的历史文件优先。
    if (stillInUnit(unit) && (!createdSeq || currentUnit()?.registrySeq === createdSeq)) {
      if (revision === manualSelectionRevision) requestedSeq.value = imported.seq;
      toast('原型已导入', 'ok');
    }
    await refresh();
  } catch (e) {
    if (stillInUnit(unit)) uploadError.value = e.message;
  } finally {
    uploading.value = false;
  }
}

const clamp = (value) => Math.max(0, Math.min(1, value));

function pointFromPointer(event) {
  const bounds = overlay.value.getBoundingClientRect();
  return {
    x: clamp((event.clientX - bounds.left) / bounds.width),
    y: clamp((event.clientY - bounds.top) / bounds.height),
  };
}

function extendLine(point) {
  draft.value = { x1: start.x, y1: start.y, x2: point.x, y2: point.y };
}

async function finishLine(point) {
  const distinct = start.x !== point.x || start.y !== point.y;
  if (distinct) extendLine(point);
  else draft.value = null;
  start = null;
  if (!distinct) return;
  drawing.value = false;
  cursor.value = null;
  await nextTick();
  note.value?.focus();
}

function pointerDown(event) {
  if (!drawing.value || pointerId !== null || event.button !== 0 || !event.isPrimary) return;
  pointerId = event.pointerId;
  cursor.value = null;
  start = pointFromPointer(event);
  overlay.value.setPointerCapture(pointerId);
  event.preventDefault();
}

function pointerMove(event) {
  if (pointerId === event.pointerId) extendLine(pointFromPointer(event));
}

function pointerUp(event) {
  if (pointerId !== event.pointerId) return;
  pointerId = null;
  finishLine(pointFromPointer(event));
}

function cancelPointer(event) {
  if (pointerId !== event.pointerId) return;
  pointerId = null;
  start = null;
  draft.value = null;
}

const keyDirections = { ArrowLeft: [-1, 0], ArrowRight: [1, 0], ArrowUp: [0, -1], ArrowDown: [0, 1] };

function drawWithKeyboard(event, top, bottom) {
  if (!drawing.value || pointerId !== null) return;
  const point = cursor.value || { x: 0.5, y: 0.5 };
  const y = cursor.value ? Math.min(bottom, Math.max(top, point.y)) : (top + bottom) / 2;
  const direction = keyDirections[event.key];
  if (direction) {
    const step = event.shiftKey ? 0.01 : 0.05;
    cursor.value = {
      x: clamp(point.x + direction[0] * step),
      y: Math.max(top, Math.min(bottom, y + direction[1] * step * (bottom - top))),
    };
    if (start) extendLine(cursor.value);
  } else if (event.key === 'Enter' || event.key === ' ') {
    const end = { x: point.x, y };
    if (start) finishLine(end);
    else {
      start = end;
      cursor.value = end;
    }
  } else if (event.key === 'Escape') {
    cancelDrawing();
  } else {
    return;
  }
  event.preventDefault();
}

async function toggleDrawing() {
  if (drawing.value) {
    cancelDrawing();
    return;
  }
  resetDrawing();
  drawing.value = true;
  await nextTick();
  overlay.value?.focus({ preventScroll: true });
}

async function cancelDrawing() {
  resetDrawing();
  await nextTick();
  drawButton.value?.focus({ preventScroll: true });
}

async function save() {
  const opinion = text.value.trim();
  if (!opinion || !draft.value || !props.canWrite || saving.value || !selected.value) return;
  const unit = props.unit;
  if (!stillInUnit(unit)) return;
  const revision = selectionRevision;
  const body = { importSeq: selected.value.seq, line: draft.value, text: opinion };
  saving.value = true;
  saveError.value = '';
  try {
    await api.addAnnotation(body, unit);
    if (stillInUnit(unit) && revision === selectionRevision) {
      saving.value = false;
      await cancelDrawing();
      toast('批注已记录', 'ok');
    }
    await refresh();
  } catch (e) {
    if (stillInUnit(unit) && revision === selectionRevision) saveError.value = e.message;
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <section v-if="canWrite || selected" aria-labelledby="prototype-import-heading" class="card imported-prototype">
    <div class="ch import-toolbar">
      <h3 id="prototype-import-heading">导入原型评审</h3>
      <span v-if="selected" class="tag">{{ interactive ? '隔离 · 交互' : '只读 · 导入' }}</span>
      <span class="spacer" />
      <template v-if="canWrite">
        <input ref="fileInput" data-testid="prototype-import-input" type="file" hidden
          aria-label="导入原型文件" accept=".png,.jpeg,.jpg,.pdf,.html,.htm,image/png,image/jpeg,application/pdf,text/html"
          @change="upload" @cancel="pickerTarget = null" />
        <button type="button" class="btn small" :disabled="uploading" @click="openPicker">导入原型</button>
      </template>
    </div>
    <p v-if="uploading" role="status" class="small muted import-message">正在处理原型文件…</p>
    <p v-if="uploadError" role="alert" class="import-error import-message">{{ uploadError }}</p>
    <p v-if="!selected" class="small muted import-message">支持 PNG、JPEG、PDF 和 HTML，单文件上限 20 MiB。</p>

    <template v-if="selected">
      <div class="import-controls">
        <label class="import-history small muted">
          历史导入
          <select data-testid="prototype-import-select" :value="selected.seq" @change="choose">
            <option v-for="item in history" :key="item.seq" :value="item.seq">
              #{{ item.seq }} {{ item.fileName }}
            </option>
          </select>
        </label>
        <button v-if="canWrite" v-show="!interactive" ref="drawButton" type="button" class="btn small" :class="{ primary: drawing }"
          :disabled="!previewReady || saving" :aria-pressed="drawing" @click="toggleDrawing">画线批注</button>
        <button v-if="selected.mediaType === 'text/html'" type="button" class="btn small"
          :data-testid="interactive ? 'prototype-interactive-close' : 'prototype-interactive-toggle'"
          @click="interactive = !interactive">{{ interactive ? '返回只读预览' : '交互预览' }}</button>
      </div>
      <div class="small muted import-meta">
        <span>导入者 {{ selected.by === 'PA' ? 'PA 产品' : selected.by }}</span>
        <span>导入时间 {{ selected.at }}</span>
        <span v-if="selected.mediaType === 'application/pdf'">PDF 首页</span>
      </div>
      <p v-if="drawing && !interactive" id="prototype-import-draw-help" class="small muted import-message">
        拖动画线，或用方向键移动光标、按 Enter 分别确定起点和终点。按 Esc 取消。
      </p>

      <PrototypeInteractivePreview v-if="interactive" :key="selected.seq" :unit="unit" :artifact="selected" />
      <div v-show="!interactive" class="import-readonly">
        <PrototypeImportPreview :key="selected.seq" :unit="unit" :artifact="selected" @status="updatePreviewStatus"
          v-slot="{ top, bottom }">
          <svg
            ref="overlay"
            data-testid="prototype-import-lines"
            class="import-lines"
            :class="{ 'is-drawing': drawing }"
            viewBox="0 0 1 1"
            preserveAspectRatio="none"
            :tabindex="drawing ? 0 : undefined"
            :role="drawing ? 'application' : undefined"
            :aria-hidden="drawing ? undefined : true"
            :aria-label="drawing ? '画线批注区域' : undefined"
            :aria-describedby="drawing ? 'prototype-import-draw-help' : undefined"
            @pointerdown="pointerDown"
            @pointermove="pointerMove"
            @pointerup="pointerUp"
            @pointercancel="cancelPointer"
            @lostpointercapture="cancelPointer"
            @keydown="drawWithKeyboard($event, top, bottom)"
          >
            <line v-for="(annotation, i) in lines" :key="i" v-bind="annotation.line" vector-effect="non-scaling-stroke" />
            <line v-if="draft" v-bind="draft" class="draft-line" vector-effect="non-scaling-stroke" />
            <path v-if="cursor" :d="'M ' + cursor.x + ' 0 V 1 M 0 ' + cursor.y + ' H 1'" vector-effect="non-scaling-stroke" />
          </svg>
        </PrototypeImportPreview>
      </div>

      <form v-if="canWrite && draft && !drawing" v-show="!interactive" class="import-form" @submit.prevent="save">
        <div class="field">
          <label for="prototype-import-note">批注意见</label>
          <textarea id="prototype-import-note" ref="note" v-model="text" required :disabled="saving" />
        </div>
        <p v-if="saveError" role="alert" class="import-error import-message">{{ saveError }}</p>
        <div class="row">
          <button type="submit" class="btn primary small" :disabled="saving || !text.trim()">提交批注</button>
          <button type="button" class="btn small" :disabled="saving" @click="cancelDrawing">取消</button>
        </div>
      </form>

      <ul v-if="lines.length" v-show="!interactive" class="import-annotations" aria-label="导入原型批注">
        <li v-for="(annotation, i) in lines" :key="i" class="anno-item">
          <span>{{ annotation.text }}</span>
          <span class="small muted">{{ annotation.by }} · {{ annotation.at }}</span>
        </li>
      </ul>
    </template>
  </section>
</template>

<style scoped>
.imported-prototype { display: grid; grid-template-columns: minmax(0, 1fr); gap: 9px; }
.import-toolbar { flex-wrap: wrap; margin: 0; }
.import-controls { display: flex; align-items: end; flex-wrap: wrap; gap: 8px; }
.import-history { width: min(100%, 340px); }
.import-history select { display: block; margin-top: 3px; }
.import-meta { display: flex; flex-wrap: wrap; gap: 2px 14px; }
.import-readonly { display: contents; }
.import-message { margin: 0; }
.import-error { color: var(--err-3); }
.import-lines {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  pointer-events: none;
}
.import-lines.is-drawing { pointer-events: auto; touch-action: none; cursor: crosshair; }
.import-lines line { stroke: #8b36e4; stroke-width: 3; stroke-linecap: round; }
.import-lines .draft-line { stroke-dasharray: 5 3; }
.import-lines path { stroke: #8b36e4; stroke-width: 1; stroke-dasharray: 3 3; }
.import-lines:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }
.import-history select:focus-visible { outline: 2px solid var(--accent); outline-offset: 1px; }
.import-form { max-width: 560px; }
.import-annotations { list-style: none; margin: 0; padding: 0; overflow-wrap: anywhere; }
.import-annotations .anno-item { align-items: baseline; flex-wrap: wrap; }
</style>
