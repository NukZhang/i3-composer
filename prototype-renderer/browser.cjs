const { chromium } = require('playwright');

const origin = 'http://prototype.invalid/';
const renderTimeout = 8000;

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
  "frame-src 'none'",
  "worker-src 'none'",
  "frame-ancestors 'none'",
  'sandbox allow-scripts allow-same-origin',
].join('; ');

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

let browserPromise;
const closingBrowsers = new WeakMap();

function getBrowser() {
  if (!browserPromise) {
    const launching = chromium.launch({
      executablePath: process.env.CHROMIUM_PATH || '/usr/bin/chromium',
      // 默认 Docker seccomp 禁止 Chromium 建立自己的命名空间。隔离由无网、非 root 容器承担。
      chromiumSandbox: false,
      handleSIGTERM: false,
      timeout: 3500,
    });
    browserPromise = launching;
    const reset = () => { if (browserPromise === launching) browserPromise = undefined; };
    void launching.then((browser) => browser.once('disconnected', reset), reset);
  }
  return browserPromise;
}

function closeBrowser(browser) {
  if (!closingBrowsers.has(browser)) {
    // 若 Chromium 整体失去响应，则由 Compose 重启容器回收进程和临时空间。
    const watchdog = setTimeout(() => {
      console.error('prototype renderer exceeded cleanup deadline');
      process.exit(1);
    }, 2500);
    const closing = browser.close().catch(() => {}).finally(() => clearTimeout(watchdog));
    closingBrowsers.set(browser, closing);
  }
  return closingBrowsers.get(browser);
}

async function shutdownBrowser() {
  if (!browserPromise) return;
  const browser = await browserPromise.catch(() => undefined);
  if (browser) await closeBrowser(browser);
}

async function closeContext(context) {
  let watchdog;
  const recycle = new Promise((resolve) => {
    watchdog = setTimeout(() => resolve(closeBrowser(context.browser())), 2000);
  });
  try {
    await Promise.race([context.close(), recycle]);
  } catch {
    // Chromium 退出时 context 也随之关闭。
  } finally {
    clearTimeout(watchdog);
  }
}

async function withinDeadline(timeout, operation) {
  let timer;
  const deadline = Date.now() + timeout;
  const expired = new Promise((_, reject) => {
    timer = setTimeout(() => reject(new Error('render timeout')), timeout);
  });
  try {
    return await Promise.race([expired, operation(() => Math.max(1, deadline - Date.now()))]);
  } finally {
    clearTimeout(timer);
  }
}

async function loadPage(html, viewport, limit, capture) {
  let browser;
  let context;
  let abandoned = false;
  try {
    return await withinDeadline(renderTimeout, async (remaining) => {
      browser = await getBrowser();
      if (abandoned) return;
      const isolated = await browser.newContext({
        viewport,
        locale: 'zh-CN',
        offline: true,
        serviceWorkers: 'block',
        acceptDownloads: false,
      });
      if (abandoned) {
        void closeContext(isolated);
        return;
      }
      context = isolated;
      const page = await context.newPage();
      context.on('page', (child) => void child.close().catch(() => {}));
      await context.routeWebSocket('**/*', (route) => route.close());

      let source = html;
      await context.route('**/*', async (route) => {
        const request = route.request();
        if (source && request.isNavigationRequest() && request.url() === origin &&
            request.method() === 'GET' && request.frame() === page.mainFrame()) {
          const body = source;
          // 持久会话仅保留浏览器上下文，不在 Node 中缓存上传正文或再次提供合成页面。
          source = undefined;
          await route.fulfill({
            headers: {
              'content-type': 'text/html; charset=utf-8',
              'content-security-policy': contentPolicy,
              'x-content-type-options': 'nosniff',
            },
            body,
          });
          return;
        }
        await route.abort('blockedbyclient');
      });

      await page.goto(origin, { waitUntil: 'load', timeout: remaining() });
      const marker = await page.waitForFunction(renderedHeight, { ...viewport, limit }, {
        timeout: Math.min(2500, remaining()),
      });
      const height = await marker.jsonValue();
      await marker.dispose();
      const png = await capture(page, height, remaining());
      return { context, page, png };
    });
  } catch (failure) {
    abandoned = true;
    if (context) await closeContext(context);
    else if (browser) void closeBrowser(browser);
    throw failure;
  }
}

async function renderHTML(html) {
  const snapshot = await loadPage(html, { width: 1200, height: 480 }, 8192, (page, height, timeout) =>
    page.screenshot({
      type: 'png',
      fullPage: true,
      clip: { x: 0, y: 0, width: 1200, height },
      animations: 'disabled',
      timeout,
    }));
  await closeContext(snapshot.context);
  return snapshot.png;
}

module.exports = { closeContext, loadPage, renderHTML, shutdownBrowser, withinDeadline };
