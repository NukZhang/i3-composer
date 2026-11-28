const { randomBytes } = require('node:crypto');
const { closeContext, loadPage, withinDeadline } = require('./browser.cjs');

const viewport = { width: 1200, height: 800 };
const maxSessions = 4;
const idleTTL = 2 * 60 * 1000;
const maxLifetime = 15 * 60 * 1000;
const actionTimeout = 5000;
const keys = new Set([
  'Tab', 'Shift+Tab', 'Enter', 'Escape', 'Space', 'Backspace', 'Delete',
  'ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight', 'Home', 'End', 'PageUp', 'PageDown', 'Control+a',
]);

const sessions = new Map();
let opening = 0;

function validAction(action) {
  if (!action || typeof action !== 'object' || Array.isArray(action)) return false;
  const fields = Object.keys(action).length;
  const isCoordinate = (value) => typeof value === 'number' && value >= 0 && value < 1;
  const isDelta = (value) => typeof value === 'number' && Math.abs(value) <= 1600;

  switch (action.type) {
    case 'click':
      return fields === 3 && isCoordinate(action.x) && isCoordinate(action.y);
    case 'wheel':
      return fields === 5 && isCoordinate(action.x) && isCoordinate(action.y) &&
        isDelta(action.deltaX) && isDelta(action.deltaY);
    case 'key':
      return fields === 2 && keys.has(action.key);
    case 'text':
      return fields === 2 && typeof action.text === 'string' &&
        action.text.length > 0 && action.text.isWellFormed() && Buffer.byteLength(action.text) <= 2048;
    case 'frame':
      return fields === 1;
    default:
      return false;
  }
}

function screenshot(page, timeout) {
  // 只返回状态画面，避免弹窗淡入淡出时截到半透明的过渡帧。
  return page.screenshot({ type: 'png', animations: 'disabled', timeout });
}

async function openSession(readHTML) {
  if (sessions.size + opening >= maxSessions) return;
  opening++;
  try {
    const html = await readHTML();
    const frame = await loadPage(html, viewport, viewport.height, (page, _height, timeout) => screenshot(page, timeout), true);
    const id = randomBytes(16).toString('hex');
    const now = performance.now();
    const session = { id, context: frame.context, page: frame.page, opened: now, touched: now, busy: false };
    sessions.set(id, session);
    frame.context.once('close', () => sessions.delete(id));
    return { id, png: frame.png };
  } finally {
    opening--;
  }
}

function closeSession(id) {
  const session = sessions.get(id);
  if (!session) return;
  // context 回收期间继续占用配额，关闭与重开交错时也不会突破上限。
  session.closing ??= closeContext(session.context).finally(() => sessions.delete(id));
  return session.closing;
}

function expired(session, now) {
  return now - session.opened >= maxLifetime || now - session.touched >= idleTTL;
}

function getSession(id) {
  const session = sessions.get(id);
  if (!session || session.closing) return;
  if (expired(session, performance.now()) || session.page.isClosed()) {
    void closeSession(id);
    return;
  }
  return session;
}

setInterval(() => {
  const now = performance.now();
  for (const [id, session] of sessions) {
    if (expired(session, now)) void closeSession(id);
  }
}, 1000).unref();

async function applyAction(session, action) {
  const { page } = session;
  const x = Math.min(viewport.width - 1, action.x * viewport.width);
  const y = Math.min(viewport.height - 1, action.y * viewport.height);
  try {
    const png = await withinDeadline(actionTimeout, async (remaining) => {
      switch (action.type) {
        case 'click':
          await page.mouse.click(x, y);
          break;
        case 'wheel':
          await page.mouse.move(x, y);
          await page.mouse.wheel(action.deltaX, action.deltaY);
          break;
        case 'key':
          await page.keyboard.press(action.key);
          break;
        case 'text':
          await page.keyboard.insertText(action.text);
          break;
      }
      // 输入发送完成时，浏览器可能仍在处理滚动和页面更新。
      await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
      return screenshot(page, remaining());
    });
    session.touched = performance.now();
    return png;
  } catch (failure) {
    // 无法确认失败前事件是否已经生效，废弃这次会话，避免重复执行同一个动作。
    void closeSession(session.id);
    throw failure;
  }
}

module.exports = { applyAction, closeSession, getSession, openSession, validAction };
