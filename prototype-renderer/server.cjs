const { chmodSync, lstatSync, unlinkSync } = require('node:fs');
const http = require('node:http');
const { renderHTML, shutdownBrowser } = require('./browser.cjs');
const { applyAction, closeSession, getSession, openSession, validAction } = require('./interactive.cjs');

const socket = process.env.PROTOTYPE_RENDER_SOCKET || '/run/prototype-renderer/render.sock';
const maxHTMLBytes = 20 * 1024 * 1024;
const maxActionBytes = 16 * 1024;
const maxActiveRequests = 3;
const decoder = new TextDecoder('utf-8', { fatal: true });

class InputError extends Error {
  constructor(status, code) {
    super(code);
    this.status = status;
  }
}

function readBody(request, maxBytes, tooLarge) {
  if (Number(request.headers['content-length']) > maxBytes) {
    throw new InputError(413, tooLarge);
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
      if (size > maxBytes) {
        fail(413, tooLarge);
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

let active = 0;
let rendering = false;
const server = http.createServer({
  requestTimeout: 6000,
  headersTimeout: 3000,
  connectionsCheckingInterval: 1000,
  maxHeaderSize: 8192,
}, async (request, response) => {
  const isSnapshot = request.url === '/render';
  const isOpen = request.url === '/interactive';
  const id = /^\/interactive\/([0-9a-f]{32})$/.exec(request.url)?.[1];
  if (!isSnapshot && !isOpen && !id) return error(response, 404, 'not_found');
  if (id && request.method === 'DELETE') {
    await closeSession(id);
    if (response.destroyed) return;
    response.writeHead(204, { 'connection': 'close', 'cache-control': 'no-store' });
    return response.end();
  }
  if (request.method !== 'POST') {
    response.setHeader('allow', id ? 'POST, DELETE' : 'POST');
    return error(response, 405, 'method_not_allowed');
  }
  const mediaType = request.headers['content-type']?.split(';', 1)[0].trim().toLowerCase();
  const encoding = request.headers['content-encoding']?.trim().toLowerCase();
  if (mediaType !== (id ? 'application/json' : 'text/html') || (encoding && encoding !== 'identity')) {
    return error(response, 415, id ? 'json_required' : 'html_required');
  }

  const session = id && getSession(id);
  if (id && !session) return error(response, 404, 'session_not_found');
  if (active >= maxActiveRequests || (isSnapshot && rendering) || session?.busy) {
    return error(response, 503, 'renderer_busy');
  }

  // 读取正文也占用渲染位，避免并发上传和动作积压。
  active++;
  if (isSnapshot) rendering = true;
  if (session) session.busy = true;
  try {
    if (session) {
      const raw = await readBody(request, maxActionBytes, 'action_too_large');
      let action;
      try {
        action = JSON.parse(raw.toString('utf8'));
      } catch {
        throw new InputError(400, 'invalid_action');
      }
      if (!validAction(action)) throw new InputError(400, 'invalid_action');
      if (!getSession(id)) throw new InputError(404, 'session_not_found');
      const png = await applyAction(session, action);
      if (!response.destroyed) reply(response, 200, 'image/png', png);
      return;
    }
    if (isOpen) {
      const opened = await openSession(() => readBody(request, maxHTMLBytes, 'html_too_large'));
      if (!opened) return error(response, 503, 'renderer_busy');
      if (response.destroyed) return closeSession(opened.id);
      response.setHeader('x-prototype-session', opened.id);
      return reply(response, 201, 'image/png', opened.png);
    }
    const html = await readBody(request, maxHTMLBytes, 'html_too_large');
    if (response.destroyed) return;
    const png = await renderHTML(html);
    if (!response.destroyed) reply(response, 200, 'image/png', png);
  } catch (failure) {
    if (response.destroyed) return;
    if (failure instanceof InputError) error(response, failure.status, failure.message);
    else error(response, 422, 'render_failed');
  } finally {
    active--;
    if (isSnapshot) rendering = false;
    if (session) session.busy = false;
  }
});

server.maxConnections = 8;
// 客户端中途停止接收 PNG 时也要释放连接。
server.setTimeout(15000);
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
server.listen({ path: socket }, () => {
  chmodSync(socket, 0o660);
  console.log('prototype renderer listening');
});

process.once('SIGTERM', () => {
  server.close();
  server.closeAllConnections();
  void shutdownBrowser().finally(() => process.exit(0));
});
