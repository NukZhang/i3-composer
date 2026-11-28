<script setup>
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue';
import PrototypeImportPreview from './PrototypeImportPreview.vue';
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
const ANNOTATION_CHANNEL = 'i3-prototype-annotations';
const anchorPath = /^(?:[0-9]{1,6}(?:\/[0-9]{1,6})*)?$/;
const routeEncoder = new TextEncoder();
const fileInput = ref(null);
const drawButton = ref(null);
const overlay = ref(null);
const note = ref(null);
const nativeFrame = ref(null);
const nativeRoute = ref(null);
const previewReady = ref(false);
const interactive = ref(false);
const readyToDraw = computed(() => interactive.value ? nativeRoute.value !== null : previewReady.value);
const uploading = ref(false);
const saving = ref(false);
const uploadError = ref('');

const drawing = ref(false);
const snapshotReview = reactive({ box: null, text: '', error: '' });
const nativeReview = reactive({ box: null, text: '', error: '' });
const review = computed(() => interactive.value ? nativeReview : snapshotReview);
const visibleAnnotations = computed(() => {
  if (!selected.value) return [];
  return props.annotations.filter((annotation) => annotation.importSeq === selected.value.seq &&
    (interactive.value ? annotation.preview?.route === nativeRoute.value : !annotation.preview));
});
const lines = computed(() => visibleAnnotations.value.filter((annotation) => annotation.line));
const boxes = computed(() => visibleAnnotations.value.filter((annotation) => !annotation.line && validBox(annotation)));
const cursor = ref(null);
let start = null;
let pointerId = null;
let selectionRevision = 0;
let manualSelectionRevision = 0;
let pickerTarget = null;
const lifetime = new AbortController();
onMounted(() => window.addEventListener('message', receiveNativeMessage));
onBeforeUnmount(() => {
  window.removeEventListener('message', receiveNativeMessage);
  lifetime.abort();
});

function stopDrawing() {
  const pointer = pointerId;
  pointerId = null;
  if (pointer !== null && overlay.value?.hasPointerCapture(pointer)) overlay.value.releasePointerCapture(pointer);
  drawing.value = false;
  cursor.value = null;
  start = null;
}

function clearReview(target) {
  target.box = null;
  target.text = '';
  target.error = '';
}

function resetDrawing() {
  stopDrawing();
  clearReview(review.value);
}

watch(() => selected.value?.seq, (seq) => {
  lastSeq.value = seq ?? null;
  selectionRevision++;
  previewReady.value = false;
  stopDrawing();
  clearReview(snapshotReview);
  clearReview(nativeReview);
  nativeRoute.value = null;
  interactive.value = false;
}, { immediate: true, flush: 'sync' });
watch(() => props.canWrite, (allowed) => {
  if (allowed) return;
  stopDrawing();
  clearReview(snapshotReview);
  clearReview(nativeReview);
});

function updatePreviewStatus(seq, ready) {
  if (seq !== selected.value?.seq) return;
  previewReady.value = ready;
  if (!ready) {
    if (!interactive.value) stopDrawing();
    clearReview(snapshotReview);
  }
}

function togglePreview() {
  if (saving.value) return;
  if (drawing.value) resetDrawing();
  selectionRevision++;
  clearReview(nativeReview);
  nativeRoute.value = null;
  interactive.value = !interactive.value;
}

function normalizeRoute(value) {
  if (typeof value !== 'string' || value.length > 1024 || (value && !value.startsWith('#'))) return null;
  if (/[\u0000-\u001f\u007f]/.test(value) || routeEncoder.encode(value).length > 1024) return null;
  return value.split('?', 1)[0];
}

function validAnchor(value) {
  return typeof value === 'string' && value.length <= 256 && anchorPath.test(value);
}

function validBox({ x, y, w, h }) {
  return [x, y, w, h].every(Number.isFinite) && x >= 0 && y >= 0 && w > 0 && h > 0 && x + w <= 1 && y + h <= 1;
}

function nativeGeometry({ x, y, w, h, preview: { route, anchor } }) {
  return { x, y, w, h, preview: { route, anchor } };
}

function syncNativeState() {
  if (!interactive.value || nativeRoute.value === null) return;
  // iframe 仅需要定位用的几何与页面标识，意见和工作台状态留在父页。
  const annotations = boxes.value.map(nativeGeometry);
  if (nativeReview.box) annotations.push({ ...nativeGeometry(nativeReview.box), draft: true });
  nativeFrame.value?.contentWindow?.postMessage({
    channel: ANNOTATION_CHANNEL, type: 'state', drawing: props.canWrite && drawing.value, annotations,
  }, '*');
}

watch([drawing, boxes, nativeRoute, () => nativeReview.box, () => props.canWrite], syncNativeState, { flush: 'post' });

function receiveNativeMessage(event) {
  const frameWindow = nativeFrame.value?.contentWindow;
  if (!interactive.value || !frameWindow || event.source !== frameWindow || event.origin !== 'null') return;
  const data = event.data;
  if (data?.channel !== ANNOTATION_CHANNEL) return;

  if (data.type === 'ready' || data.type === 'route') {
    const route = normalizeRoute(data.route);
    if (route === null) return;
    if (route !== nativeRoute.value) {
      selectionRevision++;
      nativeRoute.value = route;
      resetDrawing();
    } else if (data.type === 'ready') {
      syncNativeState();
    }
    return;
  }

  if (!props.canWrite || !drawing.value || nativeRoute.value === null) return;
  if (data.type === 'cancel') {
    void cancelDrawing();
  } else if (data.type === 'box' && normalizeRoute(data.route) === nativeRoute.value && validAnchor(data.anchor) && validBox(data)) {
    const { x, y, w, h, anchor } = data;
    void finishBox({ x, y, w, h, preview: { route: nativeRoute.value, anchor } });
  }
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

function boxFromCorners(point) {
  const box = {
    x: Math.min(start.x, point.x),
    y: Math.min(start.y, point.y),
    w: Math.abs(start.x - point.x),
    h: Math.abs(start.y - point.y),
  };
  return validBox(box) ? box : null;
}

async function finishBox(box) {
  review.value.box = box;
  stopDrawing();
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
  if (pointerId === event.pointerId) snapshotReview.box = boxFromCorners(pointFromPointer(event));
}

function pointerUp(event) {
  if (pointerId !== event.pointerId) return;
  const box = boxFromCorners(pointFromPointer(event));
  pointerId = null;
  start = null;
  snapshotReview.box = box;
  if (box) void finishBox(box);
}

function cancelPointer(event) {
  if (pointerId !== event.pointerId) return;
  pointerId = null;
  start = null;
  snapshotReview.box = null;
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
    if (start) snapshotReview.box = boxFromCorners(cursor.value);
  } else if (event.key === 'Enter' || event.key === ' ') {
    const end = { x: point.x, y };
    if (start) {
      const box = boxFromCorners(end);
      if (box) void finishBox(box);
    } else {
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
  if (!interactive.value) {
    await nextTick();
    overlay.value?.focus({ preventScroll: true });
  }
}

async function cancelDrawing() {
  resetDrawing();
  await nextTick();
  drawButton.value?.focus({ preventScroll: true });
}

async function save() {
  const target = review.value;
  const opinion = target.text.trim();
  if (!opinion || !target.box || drawing.value || !props.canWrite || saving.value || !selected.value) return;
  const unit = props.unit;
  if (!stillInUnit(unit)) return;
  const revision = selectionRevision;
  const body = { importSeq: selected.value.seq, ...target.box, text: opinion };
  saving.value = true;
  target.error = '';
  let saved = false;
  try {
    await api.addAnnotation(body, unit);
    saved = true;
    if (stillInUnit(unit) && revision === selectionRevision) {
      resetDrawing();
      toast('批注已记录', 'ok');
    }
    await refresh();
  } catch (e) {
    if (stillInUnit(unit) && revision === selectionRevision) target.error = e.message;
  } finally {
    saving.value = false;
    await nextTick();
    if (saved && stillInUnit(unit) && revision === selectionRevision) drawButton.value?.focus({ preventScroll: true });
  }
}
</script>

<template>
  <section v-if="canWrite || selected" aria-labelledby="prototype-import-heading" class="card imported-prototype">
    <div class="ch import-toolbar">
      <h3 id="prototype-import-heading">导入原型评审</h3>
      <span v-if="selected" class="tag">{{ interactive ? '交互预览' : '只读 · 导入' }}</span>
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
        <button v-if="selected.mediaType === 'text/html'" type="button" class="btn small"
          :disabled="saving" data-testid="prototype-native-toggle" @click="togglePreview">
          {{ interactive ? '返回只读预览' : '交互预览' }}
        </button>
        <button v-if="canWrite" ref="drawButton" type="button" class="btn small" :class="{ primary: drawing }"
          :disabled="!readyToDraw || saving" :aria-pressed="drawing" data-testid="prototype-import-draw"
          @click="toggleDrawing">画框批注</button>
      </div>
      <div class="small muted import-meta">
        <span>导入者 {{ selected.by === 'PA' ? 'PA 产品' : selected.by }}</span>
        <span>导入时间 {{ selected.at }}</span>
        <span v-if="selected.mediaType === 'application/pdf'">PDF 首页</span>
      </div>
      <p v-if="drawing" id="prototype-import-draw-help" class="small muted import-message">
        拖出矩形，或用方向键移动光标并按 Enter 依次确认两个对角。按 Esc 取消。
      </p>
      <iframe v-if="interactive" :key="selected.seq" ref="nativeFrame" :title="selected.fileName + ' 的交互预览'"
        :src="api.nativePrototypeURL(unit, selected.seq)" class="native-preview" data-testid="prototype-native-frame"
        sandbox="allow-scripts allow-forms" referrerpolicy="no-referrer" />
      <div v-show="!interactive" class="review-preview">
        <PrototypeImportPreview :key="selected.seq" :unit="unit" :artifact="selected" @status="updatePreviewStatus"
          v-slot="{ top, bottom }">
          <svg
            ref="overlay"
            data-testid="prototype-import-lines"
            class="import-overlay"
            :class="{ 'is-drawing': drawing && !interactive }"
            viewBox="0 0 1 1"
            preserveAspectRatio="none"
            :tabindex="drawing && !interactive ? 0 : undefined"
            :role="drawing && !interactive ? 'application' : undefined"
            :aria-hidden="drawing && !interactive ? undefined : true"
            :aria-label="drawing && !interactive ? '画框批注区域' : undefined"
            :aria-describedby="drawing && !interactive ? 'prototype-import-draw-help' : undefined"
            @pointerdown="pointerDown"
            @pointermove="pointerMove"
            @pointerup="pointerUp"
            @pointercancel="cancelPointer"
            @lostpointercapture="cancelPointer"
            @keydown="drawWithKeyboard($event, top, bottom)"
          >
            <template v-if="!interactive">
              <line v-for="(annotation, i) in lines" :key="i" v-bind="annotation.line" vector-effect="non-scaling-stroke" />
              <g data-testid="prototype-import-boxes">
                <rect v-for="(box, i) in boxes" :key="i" :x="box.x" :y="box.y" :width="box.w" :height="box.h"
                  vector-effect="non-scaling-stroke" />
                <rect v-if="snapshotReview.box" :x="snapshotReview.box.x" :y="snapshotReview.box.y"
                  :width="snapshotReview.box.w" :height="snapshotReview.box.h" class="draft-box"
                  vector-effect="non-scaling-stroke" />
              </g>
              <path v-if="cursor" :d="'M ' + cursor.x + ' 0 V 1 M 0 ' + cursor.y + ' H 1'" vector-effect="non-scaling-stroke" />
            </template>
          </svg>
        </PrototypeImportPreview>
      </div>

      <form v-if="canWrite && review.box && !drawing" class="import-form" @submit.prevent="save">
        <div class="field">
          <label for="prototype-import-note">批注意见</label>
          <textarea id="prototype-import-note" ref="note" v-model="review.text" required :disabled="saving" />
        </div>
        <p v-if="review.error" role="alert" class="import-error import-message">{{ review.error }}</p>
        <div class="row">
          <button type="submit" class="btn primary small" :disabled="saving || !review.text.trim()">提交批注</button>
          <button type="button" class="btn small" :disabled="saving" @click="cancelDrawing">取消</button>
        </div>
      </form>

      <ul v-if="visibleAnnotations.length" class="import-annotations" aria-label="导入原型批注">
        <li v-for="(annotation, i) in visibleAnnotations" :key="i" class="anno-item">
          <span>{{ annotation.text }}</span>
          <span class="small muted">{{ annotation.by }} · {{ annotation.at }}</span>
        </li>
      </ul>
    </template>
  </section>
</template>

<style scoped>
.imported-prototype { display: grid; grid-template-columns: minmax(0, 1fr); gap: 9px; }
.review-preview { display: contents; }
.native-preview {
  width: 100%;
  height: clamp(520px, 72vh, 880px);
  border: 1px solid var(--line-g);
  border-radius: 8px;
  background: #fff;
}
.import-toolbar { flex-wrap: wrap; margin: 0; }
.import-controls { display: flex; align-items: end; flex-wrap: wrap; gap: 8px; }
.import-history { width: min(100%, 340px); }
.import-history select { display: block; margin-top: 3px; }
.import-meta { display: flex; flex-wrap: wrap; gap: 2px 14px; }
.import-message { margin: 0; }
.import-error { color: var(--err-3); }
.import-overlay {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  pointer-events: none;
}
.import-overlay.is-drawing { pointer-events: auto; touch-action: none; cursor: crosshair; }
.import-overlay line { stroke: #8b36e4; stroke-width: 3; stroke-linecap: round; }
.import-overlay rect { fill: rgb(139 54 228 / 12%); stroke: #8b36e4; stroke-width: 2; }
.import-overlay .draft-box { stroke-dasharray: 5 3; }
.import-overlay path { stroke: #8b36e4; stroke-width: 1; stroke-dasharray: 3 3; }
.import-overlay:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }
.import-history select:focus-visible { outline: 2px solid var(--accent); outline-offset: 1px; }
.import-form { max-width: 560px; }
.import-annotations { list-style: none; margin: 0; padding: 0; overflow-wrap: anywhere; }
.import-annotations .anno-item { align-items: baseline; flex-wrap: wrap; }
</style>
