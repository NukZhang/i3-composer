const assert = require('node:assert/strict');
const { spawn } = require('node:child_process');
const { once } = require('node:events');
const { mkdtemp, rm } = require('node:fs/promises');
const http = require('node:http');
const { tmpdir } = require('node:os');
const path = require('node:path');
const { after, before, test } = require('node:test');

let directory;
let socketPath;
let renderer;

before(async () => {
  directory = await mkdtemp(path.join(tmpdir(), 'prototype-renderer-test-'));
  socketPath = path.join(directory, 'render.sock');
  renderer = spawn(process.execPath, [path.join(__dirname, 'server.cjs')], {
    env: { ...process.env, PROTOTYPE_RENDER_SOCKET: socketPath },
    stdio: ['ignore', 'pipe', 'inherit'],
  });
  await Promise.race([
    once(renderer.stdout, 'data'),
    once(renderer, 'exit').then(([code]) => assert.fail(`renderer exited before listening (${code})`)),
  ]);
});

after(async () => {
  if (renderer?.exitCode === null && renderer.kill()) await once(renderer, 'exit');
  if (directory) await rm(directory, { recursive: true, force: true });
});

function request(body, headers = {}) {
  return new Promise((resolve, reject) => {
    const req = http.request({
      socketPath,
      path: '/render',
      method: 'POST',
      headers: { 'content-type': 'text/html; charset=utf-8', ...headers },
    }, (res) => {
      const chunks = [];
      res.on('data', (chunk) => chunks.push(chunk));
      res.on('error', reject);
      res.on('end', () => resolve({ status: res.statusCode, headers: res.headers, body: Buffer.concat(chunks) }));
    });
    req.on('error', reject);
    req.end(body);
  });
}

function assertPNG(result, height) {
  assert.equal(result.status, 200, result.body.toString());
  assert.equal(result.headers['content-type'], 'image/png');
  assert.equal(result.body.subarray(0, 8).toString('hex'), '89504e470d0a1a0a');
  assert.equal(result.body.readUInt32BE(16), 1200);
  assert.equal(result.body.readUInt32BE(20), height);
}

test('renders an inline module and clips content beyond the maximum screenshot height', async () => {
  const result = await request(`<!doctype html><body style="margin:0">
    <script type="module">
      setTimeout(() => {
        document.body.innerHTML = '<main style="height:10000px">离线原型</main>'
      }, 100)
    </script>`);
  assertPNG(result, 8192);
});

test('rejects pages without a visible rendered marker', async () => {
  const result = await request('<!doctype html><p hidden>source marker that must stay private</p>');
  assert.equal(result.status, 422);
  assert.match(result.headers['content-type'], /^application\/json/);
  assert.doesNotMatch(result.body.toString(), /source marker/);
});

test('blocks fetch, popups, workers and external assets even with a reachable local origin', async (t) => {
  let externalRequests = 0;
  const external = http.createServer((_req, res) => {
    externalRequests++;
    res.end('unexpected network content');
  });
  external.listen(0, '127.0.0.1');
  await once(external, 'listening');
  t.after(() => new Promise((resolve) => external.close(resolve)));
  const origin = `http://127.0.0.1:${external.address().port}`;

  const result = await request(`<!doctype html><body>
    <img src="${origin}/image.png">
    <script src="${origin}/script.js"></script>
    <script>
      const popup = window.open('about:blank')
      let workerRan = false
      try {
        const worker = new Worker(URL.createObjectURL(new Blob(['postMessage("unexpected worker")'])))
        worker.onmessage = () => { workerRan = true }
      } catch {}
      fetch('${origin}/request').then(() => {}, () => {
        setTimeout(() => {
          if (!popup && !workerRan) document.body.textContent = '已离线'
        }, 100)
      })
    </script>`);
  assertPNG(result, 480);
  assert.equal(externalRequests, 0);
});

test('reclaims a renderer running an unbounded inline script', { timeout: 14000 }, async () => {
  assert.equal((await request('<script>while (true) {}</script>')).status, 422);
  assertPNG(await request('<p>Next request</p>'), 480);
});

test('rejects inputs outside the HTML and UTF-8 body contract', async () => {
  assert.equal((await request('hello', { 'content-type': 'text/plain' })).status, 415);
  assert.equal((await request(Buffer.from([0xff]))).status, 400);
  const oversized = await request('', { 'content-length': 20 * 1024 * 1024 + 1 });
  assert.equal(oversized.status, 413);
});
