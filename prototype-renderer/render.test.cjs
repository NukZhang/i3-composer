const assert = require('node:assert/strict');
const { spawn } = require('node:child_process');
const { once } = require('node:events');
const { mkdtemp, rm } = require('node:fs/promises');
const http = require('node:http');
const { tmpdir } = require('node:os');
const path = require('node:path');
const { after, before, test } = require('node:test');
const { inflateSync } = require('node:zlib');

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

function request(body, headers = {}, url = '/render', method = 'POST') {
  return new Promise((resolve, reject) => {
    const req = http.request({
      socketPath,
      path: url,
      method,
      headers: { 'content-type': 'text/html; charset=utf-8', ...headers },
    }, (res) => {
      const chunks = [];
      res.on('data', (chunk) => chunks.push(chunk));
      res.on('error', reject);
      res.on('end', () => resolve({ status: res.statusCode, headers: res.headers, body: Buffer.concat(chunks) }));
    });
    req.on('error', reject);
    req.setTimeout(16000, () => req.destroy(new Error('renderer request timed out')));
    req.end(body);
  });
}

function assertPNG(result, height, status = 200) {
  assert.equal(result.status, status, result.body.toString());
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

function interactive(url, event) {
  return request(JSON.stringify(event), { 'content-type': 'application/json' }, url);
}

function openInteractive(html) {
  return request(html, {}, '/interactive');
}

function closeInteractive(url) {
  return request(undefined, {}, url, 'DELETE');
}

function assertStart(start) {
  assertPNG(start, 800, 201);
  assert.match(start.headers['x-prototype-session'], /^[a-f0-9]{32}$/);
  return '/interactive/' + start.headers['x-prototype-session'];
}

function topLeft(png) {
  // PNG 左上角的颜色分量均没有相邻像素，任意 PNG 行滤镜的预测值都为 0。
  assert.ok([2, 6].includes(png[25]));
  const idat = [];
  for (let offset = 8; offset < png.length;) {
    const length = png.readUInt32BE(offset);
    if (png.toString('ascii', offset + 4, offset + 8) === 'IDAT') {
      idat.push(png.subarray(offset + 8, offset + 8 + length));
    }
    offset += length + 12;
  }
  return [...inflateSync(Buffer.concat(idat)).subarray(1, 4)];
}

function click(x, y) {
  return { type: 'click', x: x / 1200, y: y / 800 };
}

test('keeps menu, form and scrolling state in each interactive screenshot', async (t) => {
  const start = await openInteractive(`<!doctype html><style>
    body { margin: 0; min-height: 2400px; background: #fff }
    #marker { position: fixed; inset: 0 auto auto 0; width: 12px; height: 12px; background: red }
    button, input { display: block; margin: 20px; width: 150px; height: 40px }
    section { position: absolute; top: 65px; left: 0 }
  </style><body>
    <div id="marker"></div>
    <button id="menu">菜单</button>
    <section id="panel" hidden><input aria-label="物资名称"></section>
    <script>
      const mark = color => marker.style.background = color
      const field = panel.firstElementChild
      menu.onclick = () => { panel.hidden = !panel.hidden; mark('green') }
      field.oninput = () => mark(field.value === '桥梁' ? 'blue' : 'yellow')
      document.onkeydown = e => { if (e.key === 'Escape') { panel.hidden = true; mark('purple') } }
      window.addEventListener('scroll', () => { if (scrollY > 100) mark('aqua') })
    </script>
  `);
  const url = assertStart(start);
  t.after(() => closeInteractive(url));
  assert.deepEqual(topLeft(start.body), [255, 0, 0]);

  const steps = [
    [click(70, 35), [0, 128, 0]],
    [click(70, 105), [0, 128, 0]],
    [{ type: 'text', text: '桥梁' }, [0, 0, 255]],
    [{ type: 'key', key: 'Control+a' }, [0, 0, 255]],
    [{ type: 'key', key: 'Backspace' }, [255, 255, 0]],
    [{ type: 'key', key: 'Escape' }, [128, 0, 128]],
    [{ type: 'wheel', x: 0.6, y: 0.5, deltaX: 0, deltaY: 500 }, [0, 255, 255]],
    [{ type: 'frame' }, [0, 255, 255]],
  ];
  for (const [event, color] of steps) {
    const frame = await interactive(url, event);
    assertPNG(frame, 800);
    assert.deepEqual(topLeft(frame.body), color, JSON.stringify(event));
  }
});

test('bounds isolated sessions and serves a static screenshot with all session slots occupied', async (t) => {
  const html = `<!doctype html><body style="background:red">
    <button style="margin:20px;height:60px;width:120px" onclick="localStorage.changed='1';document.body.style.background='blue'">更改</button>
    <script>if (localStorage.changed) document.body.style.background = 'blue'</script>`;
  const first = assertStart(await openInteractive(html));
  t.after(() => closeInteractive(first));
  const changed = await interactive(first, click(70, 50));
  assertPNG(changed, 800);
  assert.deepEqual(topLeft(changed.body), [0, 0, 255]);

  const others = await Promise.all([openInteractive(html), openInteractive(html), openInteractive(html)]);
  const urls = others.map((other) => {
    const url = assertStart(other);
    t.after(() => closeInteractive(url));
    assert.deepEqual(topLeft(other.body), [255, 0, 0]);
    return url;
  });
  assert.equal(await openInteractive(html).then((result) => result.status), 503);
  const [healthy, frozen] = await Promise.all([interactive(urls[0], { type: 'frame' }), request(html)]);
  assertPNG(healthy, 800);
  assertPNG(frozen, 480);
  assert.deepEqual(topLeft(frozen.body), [255, 0, 0]);

  assert.equal((await closeInteractive(first)).status, 204);
  assert.equal((await closeInteractive(first)).status, 204);
  assert.equal((await interactive(first, { type: 'frame' })).status, 404);
  assert.equal((await interactive(urls[0], { type: 'click', x: 2, y: 0 })).status, 400);
  assert.equal((await interactive(urls[0], { type: 'key', key: 'Control+l' })).status, 400);
});

test('blocks external requests, iframes and popups after user input and dismisses native prompts', async (t) => {
  let hits = 0;
  const target = http.createServer((_req, res) => { hits++; res.end('unexpected request'); });
  target.listen(0, '127.0.0.1');
  await once(target, 'listening');
  t.after(() => new Promise((resolve) => {
    target.close(resolve);
    target.closeAllConnections();
  }));
  const origin = 'http://127.0.0.1:' + target.address().port;
  const start = await openInteractive(`<!doctype html><body style="background:red">
    <button style="margin:20px;height:60px;width:120px" id="trigger">测试</button>
    <script>
      trigger.onclick = async () => {
        alert('should be auto dismissed')
        if (window.open('${origin}/popup')) return
        const frame = document.createElement('iframe')
        frame.src = '${origin}/frame'
        document.body.append(frame)
        try { await fetch('${origin}/fetch'); return } catch {}
        document.body.style.background = 'green'
      }
    </script>`);
  const url = assertStart(start);
  t.after(() => closeInteractive(url));
  let result = await interactive(url, click(70, 50));
  assertPNG(result, 800);
  for (let retry = 0; retry < 4 && topLeft(result.body)[0] === 255; retry++) {
    result = await interactive(url, { type: 'frame' });
    assertPNG(result, 800);
  }
  assert.deepEqual(topLeft(result.body), [0, 128, 0]);
  assert.equal(hits, 0);
});

test('recovers from one session hanging while preserving another session', { timeout: 14000 }, async (t) => {
  const broken = assertStart(await openInteractive(`<!doctype html><button
    style="margin:20px;height:60px;width:120px" onclick="while(true){}">阻塞</button>`));
  const healthy = assertStart(await openInteractive('<h1>仍可使用</h1>'));
  t.after(() => closeInteractive(healthy));
  const [failed, running] = await Promise.all([
    interactive(broken, click(70, 50)),
    interactive(healthy, { type: 'frame' }),
  ]);
  assert.equal(failed.status, 422);
  assertPNG(running, 800);
  assert.equal((await interactive(broken, { type: 'frame' })).status, 404);
  assertPNG(await interactive(healthy, { type: 'frame' }), 800);
});
