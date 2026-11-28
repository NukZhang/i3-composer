<script setup>
import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue';
import { api } from '../api.js';

const props = defineProps({
  unit: { type: String, required: true },
  artifact: { type: Object, required: true },
});

const WIDTH = 1200;
const HEIGHT = 800;
const MAX_DELTA = 1600;
const MAX_TEXT = 512;
const MAX_ACTIONS = 24;
const REQUEST_TIMEOUT = 20000;
const KEYS = new Set([
  'Tab', 'Enter', 'Escape', 'Backspace', 'Delete', 'ArrowUp', 'ArrowDown',
  'ArrowLeft', 'ArrowRight', 'Home', 'End', 'PageUp', 'PageDown', 'Space',
]);
const { unit, artifact: { seq, fileName } } = props;
const input = ref(null);
const refreshButton = ref(null);
const frameURL = ref('');
const busy = ref(false);
const failure = ref('');
const inputMessage = ref('');
const queue = [];
let token = '';
let stopped = false;
let actionRequest;
let decodingURL = '';
let composing = false;
let compositionTimer;

function closeSession() {
  if (!token) return;
  const session = token;
  token = '';
  // 卸载时允许关闭请求继续发送，网络中断时交给服务端会话过期回收。
  void api.closePrototypeInteractive(unit, seq, session).catch(() => {});
}

function stop() {
  stopped = true;
  queue.length = 0;
  clearTimeout(compositionTimer);
  actionRequest?.abort();
  closeSession();
  if (decodingURL) URL.revokeObjectURL(decodingURL);
  if (frameURL.value) URL.revokeObjectURL(frameURL.value);
  decodingURL = '';
  frameURL.value = '';
}

function fail(error) {
  if (stopped) return;
  failure.value = error.status === 404 && token
    ? '交互预览会话已过期，请返回只读预览后重新打开。'
    : error.message;
  stop();
}

function onPageHide() {
  fail(new Error('交互预览已关闭，请返回只读预览后重新打开。'));
}

async function request(load, controller = new AbortController()) {
  const timer = setTimeout(() => controller.abort(), REQUEST_TIMEOUT);
  try {
    return await load(controller.signal);
  } catch (error) {
    if (controller.signal.aborted && !stopped) {
      throw new Error('交互预览连接超时，请返回只读预览后重试。');
    }
    throw error;
  } finally {
    clearTimeout(timer);
  }
}

async function showFrame(frame) {
  const url = URL.createObjectURL(frame);
  decodingURL = url;
  try {
    const image = new Image();
    image.src = url;
    await image.decode().catch(() => {
      throw new Error('交互画面无法显示');
    });
    if (stopped) return;
    if (image.naturalWidth !== WIDTH || image.naturalHeight !== HEIGHT) {
      throw new Error('交互画面尺寸无效');
    }
    // 保留上一帧直到新图解码，避免刷新时屏幕闪白或点击坐标发生跳变。
    const previous = frameURL.value;
    frameURL.value = url;
    decodingURL = '';
    await nextTick();
    if (previous) URL.revokeObjectURL(previous);
  } finally {
    if (decodingURL === url) {
      URL.revokeObjectURL(url);
      decodingURL = '';
    }
  }
}

async function open() {
  try {
    // 用户在启动期间退出时，等待启动响应拿到标识后立即关闭已创建的会话。
    const opened = await request((signal) => api.openPrototypeInteractive(unit, seq, signal));
    token = opened.token;
    if (stopped) closeSession();
    else await showFrame(opened.frame);
  } catch (error) {
    fail(error);
  }
}

async function sendActions() {
  busy.value = true;
  try {
    while (queue.length && !stopped) {
      const action = queue.shift();
      actionRequest = new AbortController();
      const frame = await request((signal) => api.actPrototypeInteractive(unit, seq, token, action, signal), actionRequest);
      if (!stopped) await showFrame(frame);
    }
  } catch (error) {
    fail(error);
  } finally {
    actionRequest = undefined;
    busy.value = false;
  }
}

const boundDelta = (delta) => Math.max(-MAX_DELTA, Math.min(MAX_DELTA, delta));
const textFits = (value) => value.length <= 2 * MAX_TEXT && Array.from(value).length <= MAX_TEXT;

function enqueue(action) {
  if (stopped || !token || !frameURL.value) return;
  const last = queue[queue.length - 1];
  // 合并等待中的连续文字和同位置滚动，保留点击、按键与文本之间的顺序。
  if (last?.type === 'text' && action.type === 'text' && textFits(last.text + action.text)) {
    last.text += action.text;
  } else if (last?.type === 'wheel' && action.type === 'wheel' &&
      Math.abs(last.x - action.x) < 0.02 && Math.abs(last.y - action.y) < 0.02) {
    last.deltaX = boundDelta(last.deltaX + action.deltaX);
    last.deltaY = boundDelta(last.deltaY + action.deltaY);
  } else if (queue.length < MAX_ACTIONS) {
    queue.push(action);
  } else {
    inputMessage.value = '操作较多，请等画面更新后继续。';
    return;
  }
  inputMessage.value = '';
  if (!busy.value) void sendActions();
}

const fraction = (value) => Math.min(1 - Number.EPSILON, Math.max(0, value));

function point(event, bounds) {
  return {
    x: fraction((event.clientX - bounds.left) / bounds.width),
    y: fraction((event.clientY - bounds.top) / bounds.height),
  };
}

function clickFrame(event) {
  const bounds = event.currentTarget.getBoundingClientRect();
  input.value?.focus({ preventScroll: true });
  enqueue({ type: 'click', ...point(event, bounds) });
}

function wheelFrame(event) {
  if (event.ctrlKey || event.metaKey || event.altKey) return;
  const mode = event.deltaMode;
  if (!event.deltaX && !event.deltaY) return;
  event.preventDefault();
  const bounds = event.currentTarget.getBoundingClientRect();
  const scaleX = mode === 1 ? 16 : mode === 2 ? WIDTH : WIDTH / bounds.width;
  const scaleY = mode === 1 ? 16 : mode === 2 ? HEIGHT : HEIGHT / bounds.height;
  enqueue({
    type: 'wheel',
    ...point(event, bounds),
    deltaX: boundDelta(event.deltaX * scaleX),
    deltaY: boundDelta(event.deltaY * scaleY),
  });
}

function keyInput(event) {
  if (composing || event.isComposing) return;
  if (event.key === 'F6') {
    event.preventDefault();
    refreshButton.value?.focus({ preventScroll: true });
    return;
  }

  let key = event.key === ' ' ? 'Space' : event.key;
  if ((event.ctrlKey || event.metaKey) && !event.altKey && key.toLowerCase() === 'a') {
    key = 'Control+a';
  } else if (event.ctrlKey || event.metaKey || event.altKey || !KEYS.has(key)) {
    return;
  } else if (key === 'Tab' && event.shiftKey) {
    key = 'Shift+Tab';
  }
  event.preventDefault();
  enqueue({ type: 'key', key });
}

function sendText(text) {
  if (!text) return;
  if (!textFits(text)) {
    inputMessage.value = '一次最多发送 512 个字符，请分段输入或粘贴。';
    return;
  }
  enqueue({ type: 'text', text });
}

function textInput(event) {
  if (composing || event.isComposing || compositionTimer !== undefined) return;
  const field = event.currentTarget;
  const value = field.value;
  field.value = '';
  sendText(value);
}

function startComposition() {
  composing = true;
}

function endComposition(event) {
  composing = false;
  const field = event.currentTarget;
  // 先提交组合结果以保留和下一次按键的顺序，随后清理同轮补发的 input。
  sendText(event.data);
  clearTimeout(compositionTimer);
  compositionTimer = setTimeout(() => {
    compositionTimer = undefined;
    field.value = '';
  }, 0);
}

function pasteInput(event) {
  event.preventDefault();
  sendText(event.clipboardData?.getData('text/plain'));
}

onMounted(() => {
  window.addEventListener('pagehide', onPageHide);
  void open();
});

onBeforeUnmount(() => {
  window.removeEventListener('pagehide', onPageHide);
  stop();
});
</script>

<template>
  <section class="interactive-preview" aria-label="隔离交互预览">
    <div class="preview-controls">
      <p class="small muted">操作仅在临时预览中生效。</p>
      <button ref="refreshButton" type="button" class="btn small" data-testid="prototype-interactive-refresh"
        :disabled="!frameURL" @click="enqueue({ type: 'frame' })">刷新画面</button>
    </div>
    <p v-if="failure" class="preview-error" role="alert">{{ failure }}</p>
    <p v-else-if="!frameURL" class="small muted" role="status">正在启动交互预览…</p>
    <template v-else>
      <div class="field keyboard-input">
        <label for="prototype-interactive-input">原型键盘输入</label>
        <p id="prototype-interactive-help" class="small muted">
          点击画面选择控件。按 F6 返回页面控件，按住 Alt 可滚动工作台。
        </p>
        <textarea id="prototype-interactive-input" ref="input" rows="1" data-testid="prototype-interactive-input"
          aria-describedby="prototype-interactive-help" autocomplete="off" autocapitalize="off" spellcheck="false"
          @keydown="keyInput" @input="textInput" @paste="pasteInput"
          @compositionstart="startComposition" @compositionend="endComposition" />
        <p v-if="inputMessage" class="small muted" role="status">{{ inputMessage }}</p>
      </div>
      <div class="interactive-frame" :aria-busy="busy">
        <img :src="frameURL" :alt="fileName + ' 的交互预览画面'" width="1200" height="800" draggable="false"
          data-testid="prototype-interactive-image" @click="clickFrame" @wheel="wheelFrame" />
        <span v-if="busy" class="small frame-status">正在更新画面…</span>
      </div>
    </template>
  </section>
</template>

<style scoped>
.interactive-preview {
  display: grid;
  gap: 9px;
  width: min(100%, 1200px);
}
.preview-controls { display: flex; flex-wrap: wrap; align-items: center; gap: 6px 12px; }
.interactive-preview p { margin: 0; }
.preview-error { color: var(--err-3); }
.interactive-frame { position: relative; background: var(--bg-inset); }
.interactive-frame img {
  display: block;
  width: 100%;
  height: auto;
  cursor: pointer;
}
.interactive-frame[aria-busy="true"] img { cursor: progress; }
.frame-status {
  position: absolute;
  top: 8px;
  right: 8px;
  padding: 3px 7px;
  border: 1px solid var(--line-b);
  border-radius: 6px;
  background: var(--bg-panel);
  color: var(--tx-0);
  pointer-events: none;
}
.keyboard-input { margin: 0; max-width: 560px; }
.keyboard-input textarea { min-height: 34px; resize: vertical; }
</style>
