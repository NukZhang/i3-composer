// 项目验收使用的 Chromium 也需要 PDF.js 自带的新浏览器 API 兼容层。
import { getDocument, GlobalWorkerOptions } from 'pdfjs-dist/legacy/build/pdf.min.mjs';
import workerURL from 'pdfjs-dist/legacy/build/pdf.worker.min.mjs?url';
import { MAX_PROTOTYPE_IMPORT_BYTES } from './api.js';

GlobalWorkerOptions.workerSrc = workerURL;

const pdfAssets = import.meta.glob([
  '../node_modules/pdfjs-dist/cmaps/*.bcmap',
  '../node_modules/pdfjs-dist/standard_fonts/*.{pfb,ttf}',
  '../node_modules/pdfjs-dist/wasm/{jbig2,openjpeg}.wasm',
], { eager: true, query: '?url&no-inline', import: 'default' });
const assetDirectories = { cMapUrl: 'cmaps', standardFontDataUrl: 'standard_fonts', wasmUrl: 'wasm' };

const MAX_CANVAS_EDGE = 4096;
const MAX_CANVAS_PIXELS = 8 * 1024 * 1024;
const MAX_IMAGE_PIXELS = 16 * 1024 * 1024;

export async function renderPDFPage(file, canvas, signal, { validate = false } = {}) {
  if (file.size > MAX_PROTOTYPE_IMPORT_BYTES) throw new Error('原型文件不能超过 20 MiB');
  if (signal.aborted) throw new Error('PDF 首页处理已取消');
  const controller = new AbortController();
  let timedOut = false;

  let task;
  let rendering;
  let cleanup;
  function destroy() {
    if (!task) return;
    cleanup ??= task.destroy().catch(() => {});
    return cleanup;
  }
  function cancel() {
    controller.abort();
    rendering?.cancel();
    void destroy();
  }
  // 旧版 Safari 的 AbortController 可取消请求，组合信号和超时由此处负责。
  signal.addEventListener('abort', cancel, { once: true });
  const timeout = setTimeout(() => {
    timedOut = true;
    cancel();
  }, 15000);

  function requireActive() {
    if (controller.signal.aborted) throw new Error('PDF 首页处理已取消');
  }

  try {
    const data = new Uint8Array(await file.arrayBuffer());
    requireActive();
    task = getDocument({
      data,
      stopAtErrors: true,
      maxImageSize: MAX_IMAGE_PIXELS,
      canvasMaxAreaInBytes: MAX_CANVAS_PIXELS * 4,
      useWorkerFetch: false,
      // Vite 会给资源加指纹，按 PDF.js 请求的原文件名映射到同源产物。
      BinaryDataFactory: class {
        async fetch({ kind, filename }) {
          const url = pdfAssets['../node_modules/pdfjs-dist/' + assetDirectories[kind] + '/' + filename];
          if (!url) throw new Error('PDF 预览资源不存在');
          const response = await fetch(url, { signal: controller.signal });
          if (!response.ok) throw new Error('PDF 预览资源读取失败');
          return new Uint8Array(await response.arrayBuffer());
        }
      },
    });
    const pdf = await task.promise;
    const page = await pdf.getPage(1);
    requireActive();
    // viewport 包含首页的裁切框和旋转，两种尺寸用同一个比例缩放。
    const { width, height } = page.getViewport({ scale: 1 });
    if (!Number.isFinite(width) || !Number.isFinite(height) || width <= 0 || height <= 0) {
      throw new Error('PDF 首页尺寸无效');
    }
    const edge = Math.max(width, height);
    const displayScale = Math.min(1, MAX_CANVAS_EDGE / edge);
    const pixelScale = Math.min(
      validate ? 128 / edge : Math.min(window.devicePixelRatio || 1, 2),
      MAX_CANVAS_EDGE / edge,
      Math.sqrt(MAX_CANVAS_PIXELS / width / height),
    );
    const viewport = page.getViewport({ scale: pixelScale });
    canvas.width = Math.max(1, Math.floor(viewport.width));
    canvas.height = Math.max(1, Math.floor(viewport.height));
    rendering = page.render({ canvas, viewport });
    await rendering.promise;
    return { width: width * displayScale, height: height * displayScale };
  } catch (error) {
    if (timedOut) throw new Error('PDF 首页处理超时');
    if (error.name === 'PasswordException') throw new Error('请使用无需密码的 PDF');
    throw error;
  } finally {
    clearTimeout(timeout);
    signal.removeEventListener('abort', cancel);
    await destroy();
  }
}
