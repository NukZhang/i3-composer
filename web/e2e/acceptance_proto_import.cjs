// 用真实浏览器和独立账本验收原型导入。BASE、SHOTS 和 CHROMIUM_PATH 与其他套件一致。
const assert = require('node:assert/strict');
const { createHash } = require('node:crypto');
const { mkdirSync, readFileSync, writeFileSync } = require('node:fs');
const { basename, join } = require('node:path');
const { chromium } = require('playwright');
const { requirePubsub } = require('./require_pubsub.cjs');

const base = process.env.BASE || 'http://127.0.0.1:8080';
const unit = 'fee';
const run = (process.env.RUN_TAG || Date.now().toString(36)).replace(/[^a-z0-9]/gi, '').slice(0, 18).toLowerCase();
const otherUnit = 'proto-' + run;
const importPath = '/api/prototype-imports';
const dynamicEnabled = process.env.PROTOTYPE_DYNAMIC === '1';
const externalHTML = process.env.PROTOTYPE_REVIEW_HTML;

function pdfFixture() {
  const stream = (color) => {
    const body = color + ' rg\n30 30 540 260 re f\n';
    return '<< /Length ' + Buffer.byteLength(body) + ' >>\nstream\n' + body + 'endstream';
  };
  const page = (contents) => '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 600 320] /Resources << >> /Contents ' + contents + ' 0 R >>';
  const objects = [
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Count 2 /Kids [3 0 R 5 0 R] >>',
    page(4), stream('0.12 0.30 0.80'),
    page(6), stream('0.92 0.30 0.10'),
  ];
  let out = '%PDF-1.4\n';
  const offsets = objects.map((object, i) => {
    const offset = Buffer.byteLength(out);
    out += (i + 1) + ' 0 obj\n' + object + '\nendobj\n';
    return offset;
  });
  const xref = Buffer.byteLength(out);
  out += 'xref\n0 ' + (objects.length + 1) + '\n0000000000 65535 f \n';
  out += offsets.map((offset) => String(offset).padStart(10, '0') + ' 00000 n \n').join('');
  out += 'trailer\n<< /Size ' + (objects.length + 1) + ' /Root 1 0 R >>\nstartxref\n' + xref + '\n%%EOF\n';
  return Buffer.from(out);
}

const htmlFixture = Buffer.from(`<!doctype html>
<html lang="zh-CN"><meta charset="utf-8"><title>外部原型</title>
<style>body { margin: 0 } main { box-sizing: border-box; padding: 36px; min-height: 100vh; background: rgb(21, 48, 88); color: white }
footer { padding: 24px; background: #e9edf6; color: #142448 }</style>
<main><h1>在线预览 · 静态 HTML</h1><p id="script-status">静态文案</p>
<img id="embedded" alt="内嵌图标" src="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='36' height='36'%3E%3Ccircle cx='18' cy='18' r='16' fill='white'/%3E%3C/svg%3E"></main>
<footer>视口高度布局后的页尾</footer>
<img alt="" src="/api/health?proto-preview-egress=1">
<script>document.querySelector('#script-status').textContent = '脚本已运行';</script>
</html>`);

const dynamicFixture = Buffer.from(`<!doctype html><html lang="zh-CN"><meta charset="utf-8">
<style>body { margin: 0 } #app > main { background: #244a83; color: white; min-height: 560px; padding: 24px }</style>
<div id="app"></div><script>document.querySelector('#app').innerHTML = '<main><h1>材料入库看板</h1><p>本次入库量 80.400 吨</p></main>';</script></html>`);

function near(actual, expected, tolerance = 0.014) {
  assert.ok(Math.abs(actual - expected) <= tolerance, `${actual} 与 ${expected} 的差值超过 ${tolerance}`);
}

function boxFromCorners([x1, y1], [x2, y2]) {
  return [Math.min(x1, x2), Math.min(y1, y2), Math.abs(x2 - x1), Math.abs(y2 - y1)];
}

function boxValues({ x, y, w, h }) {
  return [x, y, w, h];
}

function assertBoxNear(actual, expected) {
  actual.forEach((value, index) => near(value, expected[index]));
}

(async () => {
  await requirePubsub(base);
  const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH });
  try {
    const page = await browser.newPage({ baseURL: base, viewport: { width: 1450, height: 1100 } });
    page.setDefaultTimeout(12000);
    await page.addInitScript(() => localStorage.setItem('i3-theme', 'light'));
    const preview = page.getByTestId('prototype-import-preview');
    const history = page.getByTestId('prototype-import-select');
    const lines = page.getByTestId('prototype-import-lines').locator('line');
    const boxes = page.getByTestId('prototype-import-boxes').locator('rect');
    const draw = page.getByTestId('prototype-import-draw');
    const notes = page.getByRole('list', { name: '导入原型批注' });
    const note = (text) => notes.getByText(text, { exact: true });
    const externalResponses = [];
    const blockedResources = [];
    const isExternalResource = (event) => event.url().includes('proto-preview-egress=1');
    page.on('response', (response) => {
      if (isExternalResource(response)) externalResponses.push(response.url());
    });
    page.on('requestfailed', (request) => {
      if (isExternalResource(request)) blockedResources.push(request.failure()?.errorText);
    });
    const request = async (method, path, status, options = {}) => {
      const response = await page.request.fetch(path, { method, ...options });
      assert.equal(response.status(), status, path + ' ' + (response.status() === status ? '' : await response.text()));
      return response;
    };
    const post = (path, status, data) => request('POST', path, status, { data });
    const role = (name) => post('/api/role', 200, { role: name });
    const state = async (id = unit) => (await request('GET', '/api/state?pbc=' + id, 200)).json();
    const readURL = (seq, id = unit) => importPath + '/' + seq + '?pbc=' + id;
    const screenshot = async (name) => {
      if (!process.env.SHOTS) return;
      mkdirSync(process.env.SHOTS, { recursive: true });
      await page.screenshot({ path: join(process.env.SHOTS, name) });
    };
    const showPrototype = async (id = unit) => {
      await page.goto('/');
      const project = page.locator('.sidebar .trow.sub').filter({ hasText: '收费与工单数字化' });
      const row = page.locator('.sidebar [data-unit="' + id + '"]');
      await project.waitFor();
      if (!await row.isVisible()) await project.click();
      const stage = page.locator('.sidebar .trow.sub2').filter({ hasText: '原型' });
      if (!await row.evaluate((element) => element.classList.contains('on')) || !await stage.isVisible()) await row.click();
      await stage.click();
    };
    const upload = async (name, mimeType, buffer) => {
      await page.getByRole('button', { name: '导入原型', exact: true }).waitFor();
      const [response] = await Promise.all([
        page.waitForResponse((res) => new URL(res.url()).pathname === importPath && res.request().method() === 'POST'),
        page.getByTestId('prototype-import-input').setInputFiles({ name, mimeType, buffer }),
      ]);
      assert.equal(response.status(), 201, await response.text());
      const receipt = await response.json();
      assert.equal(receipt.fileName, name);
      assert.equal(receipt.mediaType, mimeType);
      assert.equal(receipt.size, buffer.length);
      assert.equal(receipt.by, 'PA');
      assert.ok(receipt.at && receipt.seq > 0);
      assert.equal(receipt.digest, 'sha256:' + createHash('sha256').update(buffer).digest('hex'));
      await page.getByText('只读 · 导入', { exact: true }).waitFor();
      await page.waitForFunction((seq) => document.querySelector('[data-testid="prototype-import-select"]')?.value === String(seq), receipt.seq);
      assert.equal(await preview.getByRole('button', { name: /编辑|保存原型/ }).count(), 0);
      return receipt;
    };
    const pngOrJpeg = async (mimeType) => {
      const data = await page.evaluate((type) => {
        const canvas = document.createElement('canvas');
        canvas.width = 800;
        canvas.height = 430;
        const ctx = canvas.getContext('2d');
        ctx.fillStyle = '#243c79';
        ctx.fillRect(0, 0, 800, 430);
        ctx.fillStyle = '#fff';
        ctx.font = '42px sans-serif';
        ctx.fillText('Prototype review', 48, 103);
        ctx.fillRect(48, 150, 460, 10);
        return canvas.toDataURL(type).split(',')[1];
      }, mimeType);
      return Buffer.from(data, 'base64');
    };
    const dragBox = async (bounds, start, end) => {
      await page.mouse.move(bounds.x + start[0] * bounds.width, bounds.y + start[1] * bounds.height);
      await page.mouse.down();
      await page.mouse.move(bounds.x + end[0] * bounds.width, bounds.y + end[1] * bounds.height, { steps: 7 });
      await page.mouse.up();
    };
    const annotate = async (text, start, end) => {
      await draw.click();
      await preview.evaluate((element) => element.scrollIntoView({ block: 'start' }));
      const bounds = await preview.boundingBox();
      assert.ok(bounds && bounds.width > 100 && bounds.height > 100);
      await dragBox(bounds, start, end);
      return submitAnnotation(text);
    };
    const submitAnnotation = async (text) => {
      await page.getByLabel('批注意见', { exact: true }).fill(text);
      const [response] = await Promise.all([
        page.waitForResponse((res) => new URL(res.url()).pathname === '/api/annotations' && res.request().method() === 'POST'),
        page.getByRole('button', { name: '提交批注', exact: true }).click(),
      ]);
      assert.equal(response.status(), 201, await response.text());
      await note(text).waitFor();
      return response.request().postDataJSON();
    };
    const normalizedBox = async () => {
      await boxes.first().waitFor({ state: 'attached' });
      assert.equal(await boxes.count(), 1);
      return boxes.first().evaluate((box) => {
        const bounds = box.closest('[data-testid="prototype-import-preview"]').getBoundingClientRect();
        const transform = box.getScreenCTM();
        const ratio = (x, y) => {
          const point = new DOMPoint(x, y).matrixTransform(transform);
          return [(point.x - bounds.x) / bounds.width, (point.y - bounds.y) / bounds.height];
        };
        const x = box.x.baseVal.value;
        const y = box.y.baseVal.value;
        const [left, top] = ratio(x, y);
        const [right, bottom] = ratio(x + box.width.baseVal.value, y + box.height.baseVal.value);
        return [left, top, right - left, bottom - top];
      });
    };
    const checkImage = async (name) => {
      await preview.locator('img').waitFor();
      await page.waitForFunction(() => document.querySelector('[data-testid="prototype-import-preview"] img')?.naturalWidth === 800);
      assert.match(await preview.locator('img').getAttribute('alt'), new RegExp(name));
    };

    await role('PA');
    const initial = await state();
    const visualGate = initial.gates.all.find(({ key }) => key === 'visual');
    assert.equal(visualGate.unknown, true);
    await post('/api/units', 201, { id: otherUnit, name: externalHTML ? '材料入库 PBC · 原型验收' : '原型隔离验收', kind: 'pbc', proj: 'p1' });
    await showPrototype();

    const png = await pngOrJpeg('image/png');
    const image = await upload('收费页.png', 'image/png', png);
    await checkImage('收费页');
    assert.deepEqual([image.width, image.height], [800, 430]);
    const pngText = '矩形批注：把标题放到这里';
    const pngStart = [0.24, 0.28];
    const pngEnd = [0.62, 0.47];
    const pngBox = boxFromCorners(pngStart, pngEnd);
    const pngBody = await annotate(pngText, pngStart, pngEnd);
    assertBoxNear(await normalizedBox(), pngBox);
    assertBoxNear(boxValues(pngBody), pngBox);
    assert.equal(pngBody.preview, undefined);
    assert.equal(pngBody.line, undefined);
    assert.equal(await lines.count(), 0);
    await screenshot('issue62-png.png');
    console.log('PASS PNG 预览、权限来源和矩形批注');

    const jpeg = await upload('补充评审.jpeg', 'image/jpeg', await pngOrJpeg('image/jpeg'));
    await checkImage('补充评审');
    assert.deepEqual([jpeg.width, jpeg.height], [800, 430]);
    assert.equal(await lines.count(), 0);
    assert.equal(await boxes.count(), 0);
    const jpegText = 'JPEG 批注：核对高精度坐标回放';
    const precisionLine = { x1: 0.23999999999999999, y1: 0.28126610293847284, x2: 0.6257662921835023, y2: 0.28126610293847284 };
    await post('/api/annotations?pbc=' + unit, 201, { importSeq: jpeg.seq, text: jpegText, line: precisionLine });
    await note(jpegText).waitFor();
    await lines.first().waitFor({ state: 'attached' });
    assert.equal(await boxes.count(), 0);
    const replayLine = (await state()).annos.find(({ text }) => text === jpegText).line;
    Object.keys(precisionLine).forEach((key) => near(replayLine[key], precisionLine[key], 0.000001));
    console.log('PASS JPEG 预览、批注隔离和高精度坐标的真实账本回放');

    const pdf = await upload('跨页评审.pdf', 'application/pdf', pdfFixture());
    const canvas = preview.locator('canvas');
    await canvas.waitFor();
    await page.waitForFunction(() => {
      const canvas = document.querySelector('[data-testid="prototype-import-preview"] canvas');
      if (!canvas?.width || !canvas.height) return false;
      const color = canvas.getContext('2d').getImageData(canvas.width / 2, canvas.height / 2, 1, 1).data;
      return color[2] >= 180 && color[0] <= 70;
    });
    near(await canvas.evaluate((node) => node.width / node.height), 600 / 320);
    const pdfText = 'PDF 批注：调整读数对齐';
    const pdfStart = [0.59, 0.43];
    const pdfEnd = [0.21, 0.24];
    const pdfBox = boxFromCorners(pdfStart, pdfEnd);
    await annotate(pdfText, pdfStart, pdfEnd);
    await screenshot('issue62-pdf.png');
    await showPrototype();
    await history.selectOption(String(pdf.seq));
    await note(pdfText).waitFor();
    const beforeResize = await normalizedBox();
    const wide = await preview.evaluate((element) => element.getBoundingClientRect().width);
    await page.setViewportSize({ width: 1040, height: 1080 });
    await page.waitForFunction((width) => {
      const element = document.querySelector('[data-testid="prototype-import-preview"]');
      return element?.getBoundingClientRect().width < width - 10;
    }, wide);
    assertBoxNear(await normalizedBox(), beforeResize);
    assertBoxNear(beforeResize, pdfBox);
    await screenshot('issue62-pdf-resized.png');
    console.log('PASS PDF 首页绘制、批注刷新和窗口缩放');

    const html = await upload('可供评审.html', 'text/html', htmlFixture);
    const frame = preview.frameLocator('iframe');
    await frame.getByRole('heading', { name: '在线预览 · 静态 HTML' }).waitFor();
    assert.equal(await frame.locator('#script-status').innerText(), '静态文案');
    assert.equal(await frame.locator('main').evaluate((node) => getComputedStyle(node).backgroundColor), 'rgb(21, 48, 88)');
    assert.equal(await frame.locator('#embedded').evaluate((node) => node.naturalWidth), 36);
    assert.deepEqual(externalResponses, []);
    assert.ok(blockedResources.some((reason) => /csp/i.test(reason)));
    const scrollHTMLToFooter = async () => {
      const scroller = page.getByTestId('prototype-import-scroll');
      await scroller.evaluate((element) => { element.scrollTop = element.scrollHeight; });
      await page.waitForFunction(() => {
        const frame = document.querySelector('[data-testid="prototype-import-preview"] iframe');
        const footer = frame?.contentDocument?.querySelector('footer');
        if (!footer) return false;
        const bounds = footer.getBoundingClientRect();
        return bounds.top >= 0 && bounds.bottom <= frame.contentWindow.innerHeight + 1;
      });
      assert.equal(await frame.locator('footer').innerText(), '视口高度布局后的页尾');
    };
    await scrollHTMLToFooter();
    const htmlText = 'HTML 批注：检查页尾位置';
    const htmlStart = [0.24, 0.92];
    const htmlEnd = [0.49, 0.97];
    const htmlBox = boxFromCorners(htmlStart, htmlEnd);
    await annotate(htmlText, htmlStart, htmlEnd);
    assertBoxNear(await normalizedBox(), htmlBox);
    await screenshot('issue62-html.png');
    await showPrototype();
    await history.selectOption(String(html.seq));
    await note(htmlText).waitFor();
    await scrollHTMLToFooter();
    assertBoxNear(await normalizedBox(), htmlBox);
    console.log('PASS HTML 样式、页尾滚动、批注回放，以及脚本和外部请求隔离');

    await history.selectOption(String(image.seq));
    await note(pngText).waitFor();
    assert.equal(await note(pdfText).count(), 0);
    assert.equal(await note(htmlText).count(), 0);
    assertBoxNear(await normalizedBox(), pngBox);
    const original = await request('GET', readURL(image.seq), 200);
    assert.deepEqual(await original.body(), png);
    await request('GET', readURL(image.seq, otherUnit), 404);
    await showPrototype(otherUnit);
    assert.equal(await page.getByText('只读 · 导入', { exact: true }).count(), 0);
    console.log('PASS 历史导入保留对应批注，换单元后文件及批注隔离');

    const oversized = { name: '超限.png', mimeType: 'image/png', buffer: Buffer.alloc((20 << 20) + 1) };
    const invalid = { name: '错误.txt', mimeType: 'text/plain', buffer: Buffer.from('not a supported prototype') };
    const emptyPdf = {
      name: '空页树.pdf', mimeType: 'application/pdf',
      buffer: Buffer.from('%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\nstartxref\n9\n%%EOF\n'),
    };
    const form = (file) => ({ multipart: { file } });
    const deniedPath = importPath + '?pbc=' + unit;
    const tooLarge = await request('POST', deniedPath, 413, form(oversized));
    assert.match((await tooLarge.json()).error, /20|大小|超/);
    const bad = await request('POST', deniedPath, 400, form(invalid));
    assert.match((await bad.json()).error, /格式|类型|支持/);
    const badPdf = await request('POST', deniedPath, 400, form(emptyPdf));
    assert.match((await badPdf.json()).error, /PDF/);
    const { units } = await (await request('GET', '/api/units', 200)).json();
    const selectedUnit = units.find(({ id }) => id === unit);
    assert.ok(selectedUnit.registrySeq > 0);
    await request('POST', deniedPath + '&createdSeq=' + (selectedUnit.registrySeq + 1), 409,
      form({ name: '登记已更换.png', mimeType: 'image/png', buffer: png }));
    const badLine = { importSeq: image.seq, line: { x1: 1.1, y1: 0.4, x2: 0.7, y2: 0.4 }, text: '非法坐标' };
    await post('/api/annotations?pbc=' + unit, 400, badLine);
    const badBox = { importSeq: image.seq, x: 0.8, y: 0.4, w: 0.3, h: 0.1, text: '超出边界的矩形' };
    await post('/api/annotations?pbc=' + unit, 400, badBox);
    await role('DA');
    await request('POST', deniedPath, 403, form({ name: '越权.png', mimeType: 'image/png', buffer: png }));
    await post('/api/annotations?pbc=' + unit, 403, { ...badBox, w: 0.1 });
    await showPrototype();
    await page.getByText('只读 · 导入', { exact: true }).waitFor();
    assert.equal(await page.getByRole('button', { name: '导入原型', exact: true }).count(), 0);
    assert.equal(await draw.count(), 0);
    const final = await state();
    const receipts = final.protoImports.filter(({ seq }) => seq > initial.seq);
    assert.deepEqual(new Set(receipts.map(({ seq }) => seq)), new Set([image.seq, jpeg.seq, pdf.seq, html.seq]));
    const annos = final.annos.filter(({ importSeq }) => [image.seq, jpeg.seq, pdf.seq, html.seq].includes(importSeq));
    assert.deepEqual(annos.map(({ text }) => text), [pngText, jpegText, pdfText, htmlText]);
    assertBoxNear(boxValues(annos[0]), pngBox);
    assert.ok(Object.values(annos[1].line).every((n) => n >= 0 && n <= 1));
    assertBoxNear(boxValues(annos[2]), pdfBox);
    assertBoxNear(boxValues(annos[3]), htmlBox);
    assert.deepEqual(final.versions.proto, initial.versions.proto);
    assert.deepEqual(final.gates.all.find(({ key }) => key === 'visual'), visualGate);
    const { facts } = await (await request('GET', '/api/facts?pbc=' + unit, 200)).json();
    const featureFacts = facts.filter(({ seq, type }) => seq > initial.seq && ['prototype.imported', 'annotation.added'].includes(type));
    assert.deepEqual(featureFacts.map(({ type }) => type), [
      'annotation.added', 'prototype.imported', 'annotation.added', 'prototype.imported',
      'annotation.added', 'prototype.imported', 'annotation.added', 'prototype.imported',
    ]);
    console.log('PASS 超限、错误格式、非法坐标及越权请求不落账，原型还原维持「判不了」');
    console.log('EVIDENCE', JSON.stringify({ receipts, annos, visualGate, featureFacts }));

    if (dynamicEnabled) {
      await role('PA');
      await page.setViewportSize({ width: 1450, height: 1100 });
      await showPrototype(otherUnit);
      const image = page.getByTestId('prototype-import-snapshot');
      const snapshot = (seq) => importPath + '/' + seq + '/snapshot?pbc=' + otherUnit;
      const waitImage = async () => {
        await image.waitFor({ timeout: 21000 });
        await page.waitForFunction(() => document.querySelector('[data-testid="prototype-import-snapshot"]')?.naturalWidth === 1200);
        assert.equal(await preview.locator('iframe').count(), 0);
      };
      const dynamic = await upload('动态材料看板.html', 'text/html', dynamicFixture);
      await waitImage();
      const pixel = await image.evaluate((node) => {
        const canvas = document.createElement('canvas');
        canvas.width = canvas.height = 1;
        const ctx = canvas.getContext('2d');
        ctx.drawImage(node, 5, 5, 1, 1, 0, 0, 1, 1);
        return Array.from(ctx.getImageData(0, 0, 1, 1).data);
      });
      assert.deepEqual(pixel, [36, 74, 131, 255]);
      await screenshot('issue101-synthetic-preview.png');
      await request('GET', importPath + '/' + dynamic.seq + '/snapshot?pbc=' + unit, 404);
      console.log('PASS 动态 HTML 渲染出脚本生成的画面，工作台仅加载图片');

      const source = externalHTML ? readFileSync(externalHTML) : dynamicFixture;
      const imported = externalHTML ? await upload(basename(externalHTML), 'text/html', source) : dynamic;
      await waitImage();
      const shot = await request('GET', snapshot(imported.seq), 200);
      assert.equal(shot.headers()['content-type'], 'image/png');
      const imageBytes = await shot.body();
      if (process.env.SHOTS && externalHTML) {
        writeFileSync(join(process.env.SHOTS, 'issue101-real-render.png'), imageBytes);
      }
      const text = '动态原型评审：确认矩形刷新后仍在';
      await annotate(text, [0.23, 0.24], [0.45, 0.26]);
      const placement = await normalizedBox();
      const before = (await state(otherUnit)).annos.find((entry) => entry.importSeq === imported.seq && entry.text === text);
      assertBoxNear(boxValues(before), placement);
      await showPrototype(otherUnit);
      await history.selectOption(String(imported.seq));
      await waitImage();
      await note(text).waitFor();
      assertBoxNear(await normalizedBox(), placement);
      const after = (await state(otherUnit)).annos.find((entry) => entry.importSeq === imported.seq && entry.text === text);
      assert.deepEqual(after, before);
      assert.deepEqual(await (await request('GET', snapshot(imported.seq), 200)).body(), imageBytes);
      const { facts: dynamicFacts } = await (await request('GET', '/api/facts?pbc=' + otherUnit, 200)).json();
      const importFact = dynamicFacts.find(({ seq, type }) => seq === imported.seq && type === 'prototype.imported');
      const boxFact = dynamicFacts.find(({ seq, type }) => seq > imported.seq && type === 'annotation.added');
      assert.equal(importFact?.actor, 'PA');
      assert.equal(boxFact?.actor, 'PA');
      await note(text).scrollIntoViewIfNeeded();
      await screenshot(externalHTML ? 'issue101-real-refresh.png' : 'issue101-synthetic-refresh.png');
      console.log('PASS 快照、原导入与批注坐标刷新后保持一致');
      console.log('DYNAMIC EVIDENCE', JSON.stringify({ unit: otherUnit, importSeq: importFact.seq, annotationSeq: boxFact.seq, box: boxValues(after), pngBytes: imageBytes.length }));

      const toggle = page.getByTestId('prototype-native-toggle');
      await toggle.click();
      const iframe = page.getByTestId('prototype-native-frame');
      const live = iframe.contentFrame();
      await iframe.waitFor();
      assert.equal(await iframe.getAttribute('sandbox'), 'allow-scripts allow-forms');
      assert.equal(await iframe.evaluate((node) => node.contentDocument), null);
      if (externalHTML) {
        await live.getByText('物资管理', { exact: true }).first().click();
        await live.getByText('入库管理', { exact: true }).first().click();
        const rows = live.locator('tbody tr').filter({ hasText: /DY-/ });
        await rows.first().waitFor();
        const initialRows = await rows.count();
        const filter = live.getByPlaceholder('输入关键词搜索');
        await filter.fill('i3-无匹配实例-105');
        await live.getByText('查询', { exact: true }).first().click();
        await rows.first().waitFor({ state: 'hidden' });
        await live.getByText('重置', { exact: true }).first().click();
        await rows.first().waitFor();
        assert.equal(await rows.count(), initialRows);
        await live.getByText('新建点验单', { exact: true }).first().click();
        const field = live.locator('textarea').first();
        await field.fill('原生 iframe 中文录入');
        assert.equal(await field.inputValue(), '原生 iframe 中文录入');
        await live.getByText('取消', { exact: true }).first().click();
        await field.waitFor({ state: 'hidden' });
        assert.equal(await rows.count(), initialRows);
        console.log('PASS 原生 HTML 在 iframe 内直接切换菜单、筛选、打开弹窗和填写表单');
      } else {
        await live.getByRole('heading', { name: '材料入库看板' }).waitFor();
        console.log('PASS 原生 HTML 在 iframe 内执行文件脚本并显示 DOM');
      }
      await toggle.click();
      await note(text).waitFor();
      assertBoxNear(await normalizedBox(), placement);
      assert.deepEqual(await (await request('GET', snapshot(imported.seq), 200)).body(), imageBytes);
      assert.deepEqual((await request('GET', '/api/facts?pbc=' + otherUnit, 200).then((response) => response.json())).facts, dynamicFacts);
      console.log('PASS 退出原生预览后恢复原快照与批注，预览期间账本一致');

      if (externalHTML) {
        const inbound = '#/material/inbound-hub';
        const outbound = '#/material/outbound-hub';
        const menu = (name) => live.getByText(name, { exact: true }).filter({ visible: true }).first();
        const keyword = live.getByPlaceholder('输入关键词搜索', { exact: true });
        const nativeDrawLayer = live.locator('[data-testid="prototype-native-annotation-layer"][style*="pointer-events: auto"]');
        const nativeBoxes = live.locator('[data-testid="prototype-native-annotation-box"]:not([data-draft])');
        const nativeText = '入库页评审：为点验单筛选范围补充说明';
        const dragStart = [0.12, 0.13];
        const dragEnd = [0.88, 0.84];
        const findAnnotation = async () => (await state(otherUnit)).annos.find((a) =>
          a.importSeq === imported.seq && a.text === nativeText);
        const openRoute = async (name, route) => {
          if (!await menu(name).isVisible()) await menu('物资管理').click();
          await menu(name).click();
          const frame = await (await iframe.elementHandle()).contentFrame();
          await frame.waitForURL((url) => url.hash === route);
        };
        const nativeBoxInField = async () => {
          await keyword.waitFor();
          await nativeBoxes.first().waitFor();
          assert.equal(await nativeBoxes.count(), 1);
          const field = await keyword.boundingBox();
          const box = await nativeBoxes.first().boundingBox();
          assert.ok(field && box && field.width > 60 && field.height > 20);
          return [(box.x - field.x) / field.width, (box.y - field.y) / field.height,
            box.width / field.width, box.height / field.height];
        };

        await toggle.click();
        await iframe.waitFor();
        await openRoute('入库管理', inbound);
        await keyword.scrollIntoViewIfNeeded();
        await draw.click();
        await nativeDrawLayer.waitFor();
        const field = await keyword.boundingBox();
        assert.ok(field && field.width > 60 && field.height > 20);
        await dragBox(field, dragStart, dragEnd);
        const sent = await submitAnnotation(nativeText);
        const saved = await findAnnotation();
        assert.ok(saved && !saved.line);
        assert.equal(saved.by, 'PA');
        assert.equal(saved.preview.route, inbound);
        assert.match(saved.preview.anchor, /^(?:\d+(?:\/\d+)*)?$/);
        assert.deepEqual(saved.preview, sent.preview);
        assert.deepEqual(boxValues(saved), boxValues(sent));
        assert.ok(saved.w > 0 && saved.h > 0 && saved.x >= 0 && saved.y >= 0 && saved.x + saved.w <= 1 && saved.y + saved.h <= 1);
        const originalPlacement = await nativeBoxInField();
        const expectedPlacement = boxFromCorners(dragStart, dragEnd);
        originalPlacement.forEach((value, index) => near(value, expectedPlacement[index], 0.055));
        console.log('PASS 在原生入库页面框选范围，正文和当前路由及归一化坐标随批注落账');

        await openRoute('出库管理', outbound);
        await nativeBoxes.first().waitFor({ state: 'hidden' });
        await note(nativeText).waitFor({ state: 'hidden' });
        await openRoute('入库管理', inbound);
        await note(nativeText).waitFor();
        assertBoxNear(await nativeBoxInField(), originalPlacement);

        await showPrototype(otherUnit);
        await history.selectOption(String(imported.seq));
        await waitImage();
        await note(nativeText).waitFor({ state: 'hidden' });
        assertBoxNear(await normalizedBox(), placement);
        await toggle.click();
        await iframe.waitFor();
        await openRoute('入库管理', inbound);
        await note(nativeText).waitFor();
        assertBoxNear(await nativeBoxInField(), originalPlacement);
        assert.deepEqual(await findAnnotation(), saved);
        console.log('PASS 切到出库页面隐藏批注，返回入库和刷新后恢复同一矩形与正文');
      }
    }
    const count = dynamicEnabled ? (externalHTML ? 12 : 10) : 6;
    console.log(`SUMMARY ${count}/${count} pass, 0 fail`);
  } finally {
    await browser.close();
  }
})().catch((error) => {
  console.error('FATAL', error);
  process.exitCode = 1;
});
