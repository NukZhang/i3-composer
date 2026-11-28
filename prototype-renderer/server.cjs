const { chmodSync, lstatSync, unlinkSync } = require('node:fs');
const http = require('node:http');
const { chromium } = require('playwright');

const socket = process.env.PROTOTYPE_RENDER_SOCKET || '/run/prototype-renderer/render.sock';
const origin = 'http://prototype.invalid/';
const viewport = { width: 1200, height: 480 };
const maxHeight = 8192;
const maxHTMLBytes = 20 * 1024 * 1024;
const renderTimeout = 8000;
const decoder = new TextDecoder('utf-8', { fatal: true });

// 允许原型自己的内联脚本、样式和数据资源，并用无法被页面移除的响应头沙箱阻止弹窗。
const contentPolicy = [
  "default-src 'none'",
  "script-src 'unsafe-inline'",
  "style-src 'unsafe-inline'",
  'img-src data: blob:',
  'font-src data: blob:',
  'media-src data: blob:',
  "base-uri 'none'",
  "form-action 'none'",
  "frame-ancestors 'none'",
  'sandbox allow-scripts allow-same-origin',
].join('; ');

class InputError extends Error {
  constructor(status, code) {
    super(code);
    this.status = status;
  }
}

function readHTML(request) {
  if (Number(request.headers['content-length']) > maxHTMLBytes) {
    throw new InputError(413, 'html_too_large');
  }

  return new Promise((resolve, reject) => {
    const chunks = [];
    let size = 0;
    const timer = setTimeout(() => fail(408, 'read_timeout'), 5000);

    function fail(status, code) {
      clearTimeout(timer);
      request.pause();
      reject(new InputError(status, code));
    }

    request.on('data', (chunk) => {
      size += chunk.length;
      if (size > maxHTMLBytes) {
        fail(413, 'html_too_large');
        return;
      }
      chunks.push(chunk);
    });
    request.once('error', () => fail(400, 'invalid_request'));
    request.once('end', () => {
      clearTimeout(timer);
      const html = Buffer.concat(chunks, size);
      try {
        decoder.decode(html);
      } catch {
        fail(400, 'invalid_utf8');
        return;
      }
      resolve(html);
    });
  });
}

// 只用当前截图范围内可见的文本和图像作为完成标记，跳过加载失败的图片、隐藏内容和空壳页面。
function renderedHeight({ width, height, limit }) {
  if (!document.body) return false;
  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_ELEMENT | NodeFilter.SHOW_TEXT);
  const range = document.createRange();
  const intersects = (rect) => rect.width > 0 && rect.height > 0 &&
    rect.right > 0 && rect.bottom > 0 && rect.left < width && rect.top < limit;
  const visible = (element) => element.checkVisibility({ opacityProperty: true, visibilityProperty: true });
  let marker;

  while ((marker = walker.nextNode())) {
    if (marker.nodeType === Node.TEXT_NODE) {
      if (!marker.textContent.trim() || !visible(marker.parentElement)) continue;
      range.selectNodeContents(marker);
      if ([...range.getClientRects()].some(intersects)) break;
    } else if (marker.matches('img, svg, canvas, video') && visible(marker)) {
      if (marker.tagName === 'IMG' && !marker.naturalWidth) continue;
      if (intersects(marker.getBoundingClientRect())) break;
    }
  }

  if (!marker) return false;
  return Math.min(limit, Math.max(height, document.body.scrollHeight, document.documentElement.scrollHeight));
}

async function renderHTML(html) {
  const deadline = Date.now() + renderTimeout;
  const remaining = () => Math.max(1, deadline - Date.now());
  // 若 Chromium 失去响应且回收超时，则由 Compose 重启容器清理进程和临时空间。
  const cleanupDeadline = setTimeout(() => {
    console.error('prototype renderer exceeded cleanup deadline');
    process.exit(1);
  }, renderTimeout + 2000);

  let browser;
  let closing;
  let timer;
  const closeBrowser = () => closing ??= browser.close().catch(() => {});
  try {
    browser = await chromium.launch({
      executablePath: process.env.CHROMIUM_PATH || '/usr/bin/chromium',
      // 默认 Docker seccomp 禁止 Chromium 建立自己的命名空间。隔离由无网、非 root 容器承担。
      chromiumSandbox: false,
      timeout: Math.min(3500, remaining()),
    });
    timer = setTimeout(closeBrowser, remaining());
    const context = await browser.newContext({
      viewport,
      locale: 'zh-CN',
      offline: true,
      serviceWorkers: 'block',
      acceptDownloads: false,
    });
    const page = await context.newPage();
    context.on('page', (child) => void child.close().catch(() => {}));
    await context.routeWebSocket('**/*', (route) => route.close());

    let served = false;
    await context.route('**/*', async (route) => {
      const request = route.request();
      if (!served && request.isNavigationRequest() && request.url() === origin &&
          request.method() === 'GET' && request.frame() === page.mainFrame()) {
        served = true;
        await route.fulfill({
          headers: {
            'content-type': 'text/html; charset=utf-8',
            'content-security-policy': contentPolicy,
            'x-content-type-options': 'nosniff',
          },
          body: html,
        });
        return;
      }
      await route.abort('blockedbyclient');
    });

    await page.goto(origin, { waitUntil: 'load', timeout: remaining() });
    const marker = await page.waitForFunction(renderedHeight, { ...viewport, limit: maxHeight }, {
      timeout: Math.min(2500, remaining()),
    });
    const height = await marker.jsonValue();
    return await page.screenshot({
      type: 'png',
      fullPage: true,
      clip: { x: 0, y: 0, width: viewport.width, height },
      animations: 'disabled',
      timeout: remaining(),
    });
  } finally {
    clearTimeout(timer);
    if (browser) await closeBrowser();
    clearTimeout(cleanupDeadline);
  }
}

function reply(response, status, type, body) {
  response.writeHead(status, {
    'connection': 'close',
    'content-type': type,
    'content-length': Buffer.byteLength(body),
    'cache-control': 'no-store',
    'x-content-type-options': 'nosniff',
  });
  response.end(body);
}

function error(response, status, code) {
  reply(response, status, 'application/json', JSON.stringify({ error: code }));
}

let busy = false;
const server = http.createServer({
  requestTimeout: 6000,
  headersTimeout: 3000,
  connectionsCheckingInterval: 1000,
  maxHeaderSize: 8192,
}, async (request, response) => {
  if (request.url !== '/render') return error(response, 404, 'not_found');
  if (request.method !== 'POST') {
    response.setHeader('allow', 'POST');
    return error(response, 405, 'method_not_allowed');
  }
  const mediaType = request.headers['content-type']?.split(';', 1)[0].trim().toLowerCase();
  const encoding = request.headers['content-encoding']?.trim().toLowerCase();
  if (mediaType !== 'text/html' || (encoding && encoding !== 'identity')) {
    return error(response, 415, 'html_required');
  }
  if (busy) return error(response, 503, 'renderer_busy');

  // 读取正文时也占用渲染位，避免并发上传累积内存。
  busy = true;
  try {
    const html = await readHTML(request);
    if (response.destroyed) return;
    const png = await renderHTML(html);
    if (!response.destroyed) reply(response, 200, 'image/png', png);
  } catch (failure) {
    if (response.destroyed) return;
    if (failure instanceof InputError) error(response, failure.status, failure.message);
    else error(response, 422, 'render_failed');
  } finally {
    busy = false;
  }
});

server.maxConnections = 8;
server.on('clientError', (failure, client) => {
  if (!client.writable) return client.destroy();
  const timedOut = failure.code === 'ERR_HTTP_REQUEST_TIMEOUT';
  const status = timedOut ? '408 Request Timeout' : '400 Bad Request';
  const body = JSON.stringify({ error: timedOut ? 'read_timeout' : 'invalid_request' });
  client.end(`HTTP/1.1 ${status}\r\nConnection: close\r\nContent-Type: application/json\r\nContent-Length: ${body.length}\r\n\r\n${body}`);
});
server.on('error', () => {
  console.error('prototype renderer could not listen');
  process.exitCode = 1;
});

// Docker 重启后卷里可能保留旧 socket。其余文件类型视为部署错误。
if (lstatSync(socket, { throwIfNoEntry: false })?.isSocket()) unlinkSync(socket);
server.listen(socket, () => {
  chmodSync(socket, 0o660);
  console.log('prototype renderer listening');
});
