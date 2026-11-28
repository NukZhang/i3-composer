// 验收入库原型的隔离交互。PROTOTYPE_REVIEW_HTML 可指向本机原型，脚本只输出检查结论。
const assert = require('node:assert/strict');
const { randomBytes } = require('node:crypto');
const { mkdirSync, readFileSync } = require('node:fs');
const { basename, join } = require('node:path');
const { chromium } = require('playwright');
const { requirePubsub } = require('./require_pubsub.cjs');

const base = process.env.BASE || 'http://127.0.0.1:8080';
const run = (process.env.RUN_TAG || Date.now().toString(36)).replace(/[^a-z0-9]/gi, '').toLowerCase().slice(0, 12);
const unit = 'proto103-' + run + randomBytes(1).toString('hex');
const source = process.env.PROTOTYPE_REVIEW_HTML;
const shots = process.env.SHOTS;
const imports = '/api/prototype-imports';
const width = 1200;
const height = 800;
const storageKey = 'i3-prototype-check-103';
const queryText = '缺项-103';
const formText = '验收录入-103';
const probeURL = new URL('/api/health?prototype-interactive-check-103=1', base).href;

// 实色标记来自页面正常事件的状态，像素断言不依赖字体、操作系统或整张快照的编码。
const fixture = Buffer.from(`<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>入库交互验收</title>
<style>
  body { margin: 0; background: #fafafa; color: #172033; font: 18px sans-serif; }
  button, input { box-sizing: border-box; font: inherit; }
  button { background: #244a83; color: white; border: 0; cursor: pointer; }
  nav, main, #isolation, #saved { position: absolute; }
  nav { left: 20px; top: 20px; }
  nav button { display: block; width: 140px; height: 42px; margin-bottom: 8px; }
  [hidden] { display: none !important; }
  main { left: 200px; top: 20px; width: 900px; height: 560px; background: #dde8f2; }
  main.ready { background: #ddf1e0; }
  h1 { margin-left: 20px; }
  #filter, #reset, #new { position: absolute; top: 85px; height: 40px; }
  #filter { left: 20px; } #query { width: 210px; height: 40px; }
  #reset { left: 315px; width: 70px; } #new { left: 410px; width: 120px; }
  #results { position: absolute; top: 150px; left: 20px; width: 500px; height: 68px; background: #195d38; color: white; }
  #results.empty { background: #8c253d; }
  #isolation, #saved { left: 1000px; width: 35px; height: 28px; }
  #isolation { top: 45px; background: #8c253d; }
  #isolation.safe, #saved.saved { background: #195d38; }
  #saved { top: 85px; background: #dde8f2; }
  dialog { position: fixed; top: 270px; left: 410px; margin: 0; width: 380px; height: 160px; padding: 0; border: 0; background: #ffebbb; }
  dialog h2 { margin: 15px 20px; }
  dialog input { position: absolute; top: 70px; left: 20px; width: 205px; height: 38px; }
  dialog button { position: absolute; top: 70px; left: 240px; width: 100px; height: 38px; }
</style>
<nav><button id="menu">物资管理</button><button id="inbound" hidden>入库管理</button></nav>
<main id="screen"><h1 id="title">工作台</h1>
  <section id="receiving" hidden>
    <form id="filter"><input id="query" aria-label="输入关键词搜索"><button type="submit">查询</button></form>
    <button id="reset">重置</button><button id="new">新建点验单</button>
    <div id="results">两条记录</div>
  </section>
</main>
<span id="isolation" aria-label="隔离检查"></span><span id="saved" aria-label="保存结果"></span>
<dialog id="dialog"><h2>新建点验单</h2><form id="editor"><input id="description" aria-label="点验说明"><button type="submit">保存</button></form></dialog>
<script>
  const byId = id => document.getElementById(id);
  byId('menu').onclick = () => { byId('inbound').hidden = false; };
  byId('inbound').onclick = () => { location.hash = '/inbound'; };
  addEventListener('hashchange', () => {
    byId('receiving').hidden = location.hash !== '#/inbound';
    byId('screen').classList.toggle('ready', !byId('receiving').hidden);
    byId('title').textContent = '入库管理';
  });
  byId('filter').onsubmit = event => {
    event.preventDefault();
    if (byId('query').value !== ${JSON.stringify(queryText)}) return;
    byId('results').classList.add('empty');
    byId('results').textContent = '无匹配记录';
  };
  byId('reset').onclick = () => {
    byId('query').value = '';
    byId('results').classList.remove('empty');
    byId('results').textContent = '两条记录';
  };
  byId('new').onclick = () => byId('dialog').showModal();
  byId('editor').onsubmit = event => {
    event.preventDefault();
    if (byId('description').value !== ${JSON.stringify(formText)}) return;
    localStorage.setItem(${JSON.stringify(storageKey)}, 'sandbox');
    byId('saved').classList.add('saved');
    byId('dialog').close();
  };
  fetch(${JSON.stringify(probeURL)}).catch(() => {
    if (localStorage.getItem(${JSON.stringify(storageKey)}) === null && !document.cookie.includes('i3_session_probe')) {
      byId('isolation').classList.add('safe');
    }
  });
</script></html>`);

function pixelDifference(before, after) {
  assert.equal(before.length, after.length);
  let changed = 0;
  for (let i = 0; i < before.length; i += 4) {
    if (Math.abs(before[i] - after[i]) >= 12 || Math.abs(before[i + 1] - after[i + 1]) >= 12 ||
      Math.abs(before[i + 2] - after[i + 2]) >= 12) changed++;
  }
  return changed;
}

(async () => {
  await requirePubsub(base);
  const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH });
  try {
    const page = await browser.newPage({ baseURL: base, viewport: { width: 1500, height: 1100 } });
    page.setDefaultTimeout(16000);
    await page.addInitScript((key) => {
      localStorage.setItem('i3-theme', 'light');
      localStorage.setItem(key, 'workbench');
      document.cookie = 'i3_session_probe=workbench; SameSite=Lax; Path=/';
    }, storageKey);
    const imageId = 'prototype-interactive-image';
    const image = page.getByTestId(imageId);
    const input = page.getByTestId('prototype-interactive-input');
    const close = page.getByTestId('prototype-interactive-close');
    const toggle = page.getByTestId('prototype-interactive-toggle');
    const snapshot = page.getByTestId('prototype-import-snapshot');
    const evalErrors = [];
    page.on('pageerror', (error) => evalErrors.push(error.message));
    const request = async (method, path, status, data) => {
      const response = await page.request.fetch(path, { method, data });
      assert.equal(response.status(), status, path + ' ' + (response.status() === status ? '' : await response.text()));
      return response;
    };
    const responseWhile = async (method, pathname, operation) => {
      const [response] = await Promise.all([
        page.waitForResponse((res) => new URL(res.url()).pathname === pathname && res.request().method() === method),
        operation(),
      ]);
      return response;
    };
    const upload = async (name, buffer) => {
      const emitted = await responseWhile('POST', imports,
        () => page.getByTestId('prototype-import-input').setInputFiles({ name, mimeType: 'text/html', buffer }));
      assert.equal(emitted.status(), 201, await emitted.text());
      const { seq } = await emitted.json();
      await expectSnapshot();
      return seq;
    };
    const expectSnapshot = async () => {
      await snapshot.waitFor();
      assert.equal(await page.getByTestId('prototype-import-preview').locator('iframe').count(), 0);
    };
    const waitFrame = async (previous = '') => {
      await page.waitForFunction(({ id, previous, width, height }) => {
        const img = document.querySelector('[data-testid="' + id + '"]');
        return img?.complete && img.src !== previous && img.naturalWidth === width && img.naturalHeight === height;
      }, { id: imageId, previous, width, height });
    };
    const region = (x, y, w, h) => image.evaluate((node, [x, y, w, h]) => {
      const canvas = document.createElement('canvas');
      canvas.width = w;
      canvas.height = h;
      const ctx = canvas.getContext('2d', { willReadFrequently: true });
      ctx.drawImage(node, x, y, w, h, 0, 0, w, h);
      return Array.from(ctx.getImageData(0, 0, w, h).data);
    }, [x, y, w, h]);
    const expectPixel = async (point, color) => assert.deepEqual(await region(...point, 1, 1), [...color, 255], `像素坐标 ${point}`);
    const start = async (seq) => {
      const opened = await responseWhile('POST', `${imports}/${seq}/interactive`, () => toggle.click());
      assert.equal(opened.status(), 201);
      assert.equal(opened.headers()['content-type'], 'image/png');
      const token = opened.headers()['x-prototype-session'];
      assert.ok(token);
      await waitFrame();
      assert.equal(await page.getByTestId('prototype-import-preview').locator('iframe').count(), 0);
      return `${imports}/${seq}/interactive/${encodeURIComponent(token)}`;
    };
    const controls = (session) => {
      const action = async (operation) => {
        const previous = await image.getAttribute('src');
        const response = await responseWhile('POST', session, operation);
        assert.equal(response.status(), 200);
        assert.equal(response.headers()['content-type'], 'image/png');
        await waitFrame(previous);
        return response.request().postDataJSON();
      };
      return {
        click: async (x, y) => {
          await image.scrollIntoViewIfNeeded();
          const bounds = await image.boundingBox();
          return action(() => page.mouse.click(bounds.x + x * bounds.width / width, bounds.y + y * bounds.height / height));
        },
        type: async (text) => {
          assert.ok(await input.evaluate((node) => node === document.activeElement), '画面点击后工作台应接收键盘输入');
          return action(() => page.keyboard.insertText(text));
        },
        key: (key) => action(() => input.press(key)),
        end: async () => {
          const stopped = await responseWhile('DELETE', session, () => close.click());
          assert.equal(stopped.status(), 204);
          await image.waitFor({ state: 'hidden' });
          await expectSnapshot();
        },
      };
    };

    await request('POST', '/api/role', 200, { role: 'PA' });
    await request('POST', '/api/units', 201, { id: unit, name: '交互原型验收 ' + unit, kind: 'pbc', proj: 'p1' });
    await page.goto('/');
    const row = page.locator(`.sidebar [data-unit="${unit}"]`);
    const project = page.locator('.sidebar .trow.sub').filter({ hasText: '收费与工单数字化' });
    await project.waitFor();
    if (!await row.isVisible()) await project.click();
    await row.click();
    await page.locator('.sidebar .trow.sub2').filter({ hasText: '原型' }).click();

    const seq = await upload('交互验收样例.html', fixture);
    const factsURL = '/api/facts?pbc=' + unit;
    const snapshotURL = `${imports}/${seq}/snapshot?pbc=${unit}`;
    const beforeFacts = await (await request('GET', factsURL, 200)).json();
    const beforeSnapshot = await (await request('GET', snapshotURL, 200)).body();
    const session = await start(seq);
    const ui = controls(session);
    await expectPixel([1015, 58], [25, 93, 56]);
    await expectPixel([34, 84], [250, 250, 250]);
    const click = await ui.click(80, 41);
    await expectPixel([34, 84], [36, 74, 131]);
    await request('POST', session + '?pbc=fee', 404, click);
    await ui.click(80, 90);
    await expectPixel([800, 300], [221, 241, 224]);
    console.log('PASS 交互图片中的菜单展开并进入入库页面，另一个单元无法使用该会话');

    await expectPixel([670, 206], [25, 93, 56]);
    await ui.click(300, 125);
    await ui.type(queryText);
    await ui.key('Enter');
    await expectPixel([670, 206], [140, 37, 61]);
    await ui.click(550, 125);
    await expectPixel([670, 206], [25, 93, 56]);
    console.log('PASS 键盘筛选改变结果，点击重置恢复原列表');

    await ui.click(650, 125);
    await expectPixel([775, 416], [255, 235, 187]);
    await ui.key('Escape');
    await expectPixel([775, 416], [221, 241, 224]);
    await ui.click(650, 125);
    await ui.click(510, 359);
    await ui.type(formText);
    await ui.click(699, 359);
    await expectPixel([775, 416], [221, 241, 224]);
    await expectPixel([1015, 97], [25, 93, 56]);
    assert.equal(await page.evaluate((key) => localStorage.getItem(key), storageKey), 'workbench');
    if (shots) {
      mkdirSync(shots, { recursive: true });
      await page.screenshot({ path: join(shots, 'issue103-synthetic-interactive.png') });
    }
    console.log('PASS 弹窗打开和退出，中文输入在沙箱中触发表单保存');

    await ui.end();
    await request('POST', session + '?pbc=' + unit, 404, click);
    const reopened = await start(seq);
    assert.notEqual(reopened, session);
    await expectPixel([1015, 58], [25, 93, 56]);
    await expectPixel([1015, 97], [221, 232, 242]);
    await expectPixel([34, 84], [250, 250, 250]);
    await controls(reopened).end();
    assert.deepEqual(await (await request('GET', factsURL, 200)).json(), beforeFacts);
    assert.deepEqual(await (await request('GET', snapshotURL, 200)).body(), beforeSnapshot);
    console.log('PASS 隔离浏览器的网络、Cookie 和存储检查通过，关闭和重开不改变快照与账本');

    if (source) {
      const real = controls(await start(await upload(basename(source), readFileSync(source))));
      // 坐标由同版的无网 Chromium 以 1200×800 独立核对真实 DOM 后记录。
      const menuArea = [25, 205, 105, 84];
      const tableArea = [310, 364, 600, 150];
      const dialogArea = [320, 145, 550, 280];
      const formArea = [395, 394, 215, 54];
      const menuBefore = await region(...menuArea);
      await real.click(67, 161);
      assert.ok(pixelDifference(menuBefore, await region(...menuArea)) > 150, '展开物资管理后应显示子菜单');
      const homePanel = await region(...tableArea);
      await real.click(68, 265);
      const inboundTable = await region(...tableArea);
      assert.ok(pixelDifference(homePanel, inboundTable) > 1000, '点击入库管理后主区域应切换到台账');
      await real.click(352, 237);
      await real.type('i3-preview-unmatched-103');
      await real.click(294, 277);
      assert.ok(pixelDifference(inboundTable, await region(...tableArea)) > 300, '关键词应筛除台账记录');
      await real.click(1134, 237);
      assert.ok(pixelDifference(inboundTable, await region(...tableArea)) < 200, '重置应恢复台账记录');

      const beforeDialog = await region(...dialogArea);
      await real.click(1131, 136);
      assert.ok(pixelDifference(beforeDialog, await region(...dialogArea)) > 3000, '新建点验单应打开弹窗');
      await real.click(655, 433);
      const beforeText = await region(...formArea);
      await real.type('临时表单验证');
      assert.ok(pixelDifference(beforeText, await region(...formArea)) > 50, '点验说明应像素可见地更新');
      const openDialog = await region(...dialogArea);
      await real.click(845, 511);
      assert.ok(pixelDifference(openDialog, await region(...dialogArea)) > 3000, '取消后弹窗应消失');
      // 取消后的指针仍在台账上，移除弹窗和悬停效果会影响部分像素。
      assert.ok(pixelDifference(inboundTable, await region(...tableArea)) < 6500, '取消后应恢复未修改的台账');
      await real.end();
      console.log('PASS 本机物资原型完成菜单导航、关键词筛选、打开点验单、中文填写和取消');
    }
    assert.deepEqual(evalErrors, []);
    const count = source ? 5 : 4;
    console.log(`SUMMARY ${count}/${count} pass, 0 fail`);
  } finally {
    await browser.close();
  }
})().catch((error) => {
  console.error('FATAL', error);
  process.exitCode = 1;
});
