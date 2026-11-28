<script setup>
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue';
import { api } from '../api.js';

const props = defineProps({
  unit: { type: String, required: true },
  artifact: { type: Object, required: true },
});
const emit = defineEmits(['status']);

const HTML_WIDTH = 1200;
const HTML_HEIGHT = 480;
const HTML_MAX_HEIGHT = 8192;
const { mediaType, fileName, seq } = props.artifact;
const isImage = mediaType === 'image/png' || mediaType === 'image/jpeg';
const isPDF = mediaType === 'application/pdf';
const isHTML = mediaType === 'text/html';
const url = api.prototypeImportURL(props.unit, seq);

const preview = ref(null);
const scrollport = ref(null);
const iframe = ref(null);
const canvas = ref(null);
const dimensions = ref(null);
const frameScale = ref(1);
const frameScrollY = ref(0);
const clipped = ref(false);
const error = ref('');
const controller = new AbortController();
let resizeObserver;
let timeout;

const sizeStyle = computed(() => {
  if (isHTML) return { height: HTML_HEIGHT * frameScale.value + 'px' };
  if (!dimensions.value) return { width: '100%' };
  const { width, height } = dimensions.value;
  // 放大小图以便落点，保持原始比例并限制极端长图占用的页面高度。
  const displayWidth = Math.min(Math.max(width, 360), 8192 * width / height);
  return { width: displayWidth + 'px', aspectRatio: width + ' / ' + height };
});
const frameStyle = computed(() => ({
  width: HTML_WIDTH + 'px',
  height: HTML_HEIGHT + 'px',
  transform: 'scale(' + frameScale.value + ')',
}));
const canScroll = computed(() => isHTML && dimensions.value?.height > HTML_HEIGHT);
const scrollStyle = computed(() => isHTML ? {
  '--html-viewport-height': (dimensions.value ? HTML_HEIGHT * frameScale.value : 0) + 'px',
  '--html-scroll-distance': (canScroll.value ? (dimensions.value.height - HTML_HEIGHT) * frameScale.value : 0) + 'px',
} : undefined);
const annotationStyle = computed(() => ({
  height: dimensions.value.height * frameScale.value + 'px',
  transform: 'translateY(' + (-frameScrollY.value * frameScale.value) + 'px)',
}));
const visibleTop = computed(() => frameScrollY.value / dimensions.value.height);
const visibleBottom = computed(() => Math.min(1, (frameScrollY.value + HTML_HEIGHT) / dimensions.value.height));

function syncFrameScroll() {
  const frame = iframe.value?.contentWindow;
  if (!frame || !canScroll.value || !frameScale.value) return;
  const max = dimensions.value.height - HTML_HEIGHT;
  const offset = Math.max(0, Math.min(max, scrollport.value.scrollTop / frameScale.value));
  frame.scrollTo({ top: offset, left: 0, behavior: 'instant' });
  frameScrollY.value = Math.max(0, Math.min(max, frame.scrollY));
}

async function resizeHTML(width) {
  const scale = width / HTML_WIDTH;
  if (!scale || scale === frameScale.value) return;
  const documentY = frameScrollY.value;
  frameScale.value = scale;
  await nextTick();
  if (!canScroll.value || !scrollport.value) return;
  scrollport.value.scrollTop = documentY * scale;
  syncFrameScroll();
}

function ready(width, height) {
  if (controller.signal.aborted) return;
  clearTimeout(timeout);
  dimensions.value = { width, height };
  emit('status', seq, true);
}

function fail(message) {
  if (controller.signal.aborted) return;
  error.value = message;
  dimensions.value = null;
  clipped.value = false;
  clearTimeout(timeout);
  controller.abort();
  emit('status', seq, false);
}

function imageLoaded(event) {
  const { naturalWidth, naturalHeight } = event.currentTarget;
  if (naturalWidth && naturalHeight) ready(naturalWidth, naturalHeight);
  else fail('图片尺寸读取失败');
}

async function frameLoaded(event) {
  if (controller.signal.aborted) return;
  const frame = event.currentTarget;
  try {
    const doc = frame.contentDocument;
    if (!doc || doc.contentType !== 'text/html' || doc.URL !== new URL(url, location.href).href) {
      fail('静态 HTML 预览加载失败');
      return;
    }
    if (dimensions.value) return;
    await doc.fonts.ready;
    if (frame.contentDocument !== doc || controller.signal.aborted) return;
    // 视口宽高总是固定。滚动区按文档高度展开，100vh 后的内容不会随测量结果继续下移。
    const height = Math.max(
      doc.documentElement.scrollHeight,
      doc.body?.scrollHeight || 0,
      Math.ceil(doc.body?.getBoundingClientRect().bottom || 0),
    );
    clipped.value = height > HTML_MAX_HEIGHT;
    frameScale.value = preview.value.getBoundingClientRect().width / HTML_WIDTH;
    ready(HTML_WIDTH, Math.min(HTML_MAX_HEIGHT, Math.max(HTML_HEIGHT, height)));
  } catch {
    fail('静态 HTML 预览加载失败');
  }
}

async function loadPDF() {
  try {
    const [file, { renderPDFPage }] = await Promise.all([
      api.prototypeImportFile(props.unit, seq, controller.signal),
      import('../prototypePDF.js'),
    ]);
    if (controller.signal.aborted) return;
    const size = await renderPDFPage(file, canvas.value, controller.signal);
    ready(size.width, size.height);
  } catch (e) {
    fail('PDF 首页预览失败。' + e.message);
  }
}

onMounted(() => {
  if (dimensions.value || error.value) return;
  timeout = setTimeout(() => fail('预览加载超时，请重新打开该文件'), 20000);
  if (isPDF) {
    void loadPDF();
  } else if (isHTML) {
    resizeObserver = new ResizeObserver(([entry]) => {
      void resizeHTML(entry.contentRect.width);
    });
    resizeObserver.observe(preview.value);
  } else if (!isImage) {
    fail('该文件格式无法预览');
  }
});

onBeforeUnmount(() => {
  clearTimeout(timeout);
  controller.abort();
  resizeObserver?.disconnect();
});
</script>

<template>
  <p v-if="error" class="preview-status" role="alert">{{ error }}</p>
  <p v-else-if="!dimensions" class="small muted preview-status" role="status">正在加载预览…</p>
  <div
    ref="scrollport"
    :class="{ 'html-scroll': isHTML }"
    :style="scrollStyle"
    :data-testid="isHTML && dimensions ? 'prototype-import-scroll' : undefined"
    :tabindex="canScroll ? 0 : undefined"
    :role="canScroll ? 'region' : undefined"
    :aria-label="canScroll ? fileName + ' 预览滚动区' : undefined"
    @scroll="syncFrameScroll"
  >
    <div
      ref="preview"
      class="import-preview"
      :class="{ 'is-ready': dimensions }"
      :style="sizeStyle"
      :data-testid="dimensions ? 'prototype-import-preview' : undefined"
    >
      <img v-if="isImage && !error" :src="url" :alt="fileName" draggable="false"
        @load="imageLoaded" @error="fail('图片预览加载失败')" />
      <canvas v-else-if="isPDF" ref="canvas" role="img" :aria-label="fileName + ' 首页'" />
      <iframe v-else-if="isHTML && !error" ref="iframe" :src="url" :title="fileName" :style="frameStyle"
        sandbox="allow-same-origin" referrerpolicy="no-referrer" scrolling="no" tabindex="-1" inert
        @load="frameLoaded" />
      <div v-if="isHTML && dimensions" class="html-annotation-page" :style="annotationStyle">
        <slot :top="visibleTop" :bottom="visibleBottom" />
      </div>
      <slot v-else-if="dimensions" :top="0" :bottom="1" />
    </div>
  </div>
  <p v-if="clipped" class="small muted preview-status">页面过长，预览展示顶部 8192 像素。</p>
</template>

<style scoped>
.import-preview {
  position: relative;
  max-width: 100%;
  overflow: hidden;
  background: #fff;
}
.import-preview:not(.is-ready) {
  height: 0;
  visibility: hidden;
}
.import-preview img, .import-preview canvas, .import-preview iframe {
  position: absolute;
  inset: 0;
  display: block;
  width: 100%;
  height: 100%;
}
.import-preview iframe {
  border: 0;
  transform-origin: top left;
  pointer-events: none;
}
.html-scroll {
  width: min(100%, 1200px);
  height: var(--html-viewport-height);
  overflow-x: hidden;
  overflow-y: auto;
}
.html-scroll::after {
  content: '';
  display: block;
  height: var(--html-scroll-distance);
}
.html-scroll .import-preview { position: sticky; top: 0; }
.html-scroll:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
.html-annotation-page { position: absolute; inset: 0 0 auto; pointer-events: none; }
.preview-status { margin: 0; }
.preview-status[role="alert"] { color: var(--err-3); }
</style>
