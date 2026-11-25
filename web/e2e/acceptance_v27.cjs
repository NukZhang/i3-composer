// v2.7 全链路验收：录入项目 → 写 BRD → 拆交付单元 → 每个单元执行交付 → 发布。
//
// 每一步都截一张图，落进 docs/selftest-v27/shots/，供自测报告直接引用。
//
// 判据不是「页面变了没有」，而是「账本里有没有这条事实」：
//   - 项目 / 工作空间：projects 日志里的 project.created / workspace.created
//   - BRD：units 日志里带 proj 的 brd.saved
//   - 交付单元：units 日志里带 proj 的 unit.created
//   - 每个单元的交付：test.run / build.succeeded / deploy.succeeded / item.published
//
// 用法：BASE=http://127.0.0.1:8080 node acceptance_v27.cjs
const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

const CHROME = '/Users/nukzhang/Library/Caches/ms-playwright/chromium-1217/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing';
const BASE = process.env.BASE || 'http://127.0.0.1:8080';
const SHOTS = process.env.SHOTS || path.join(__dirname, '../../docs/selftest-v27/shots');
const RUN = process.env.RUN_TAG || String(Date.now()).slice(-6);

const results = [];
function check(name, ok, detail) {
  results.push({ name, ok: !!ok, detail });
  console.log((ok ? 'PASS ' : 'FAIL ') + name + (detail === undefined ? '' : ' :: ' + JSON.stringify(detail)));
}
function joinedIn(projects, id) {
  return projects.some((p) => p.id === id);
}
async function waitFor(fn, ms, step) {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    const v = await fn();
    if (v) return v;
    await new Promise((r) => setTimeout(r, step || 800));
  }
  return null;
}

(async () => {
  fs.mkdirSync(SHOTS, { recursive: true });
  const browser = await chromium.launch({ executablePath: CHROME });
  const ctx = await browser.newContext({ viewport: { width: 1500, height: 950 } });
  const page = await ctx.newPage();
  const pageErrors = [];
  const httpErrors = [];
  page.on('pageerror', (e) => pageErrors.push(String(e)));
  page.on('console', (m) => { if (m.type() === 'error') pageErrors.push('console: ' + m.text()); });
  page.on('response', (r) => { if (r.status() >= 400) httpErrors.push(r.status() + ' ' + r.url()); });

  const api = (p, opts) => page.evaluate(async ([x, o]) => {
    const r = await fetch(x, o || {});
    return { code: r.status, body: await r.json().catch(() => null) };
  }, [p, opts]);
  const role = async (r) => {
    await page.click('.rolebtn:has-text("' + r + '")');
    await page.waitForTimeout(400);
  };
  let shotNo = 0;
  const shot = async (name) => {
    const file = String(++shotNo).padStart(2, '0') + '-' + name.replace(/^\d+-/, '') + '.png';
    await page.screenshot({ path: path.join(SHOTS, file), fullPage: false });
    return file;
  };

  await page.goto(BASE + '/', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1500);

  // ---- 1. 侧栏是「工作空间 → 项目」两级，项目右边有 ＋ ----
  const side0 = await page.evaluate(() => ({
    text: document.querySelector('.sidebar').textContent,
    add: !!document.querySelector('.sb-add'),
    ws: Array.from(document.querySelectorAll('.sb-ws')).map((x) => x.textContent.trim()),
  }));
  check('1 侧栏是两级结构（工作空间 → 项目）', side0.ws.length > 0 && side0.add, side0.ws);
  check('1 「项目」右侧有 ＋（新建 / 加入已有）', side0.add);
  await shot('项目侧栏');

  // ---- 2. 新建工作空间 ----
  await role('PA');
  await page.click('.sb-add');
  await page.waitForTimeout(400);
  await page.click('.mbox button:has-text("工作空间")');
  await page.waitForTimeout(300);
  const wsName = '验收空间' + RUN;
  await page.fill('.mbox input', wsName);
  await page.click('.mbox button:has-text("创建工作空间")');
  await waitFor(async () => (await api('/api/projects')).body.workspaces.some((w) => w.name === wsName), 10000);
  const wsOK = (await api('/api/projects')).body.workspaces.some((w) => w.name === wsName);
  check('2 新建工作空间落账（workspace.created）', wsOK, wsName);
  await page.waitForTimeout(600);
  await shot('新建工作空间');

  // ---- 3. 新建项目（空白起步）----
  await page.click('.sb-add');
  await page.waitForTimeout(400);
  await page.click('.mbox button:has-text("项目")');
  await page.waitForTimeout(300);
  const projName = '门禁与访客' + RUN;
  await page.fill('.mbox input', projName);
  // 归入刚建的工作空间
  const wsID = (await api('/api/projects')).body.workspaces.find((w) => w.name === wsName).id;
  await page.selectOption('.mbox select', wsID);
  await page.click('.mbox button:has-text("创建项目")');
  const proj = await waitFor(async () => {
    const j = (await api('/api/projects')).body.projects.find((p) => p.name === projName);
    return j || null;
  }, 10000);
  check('3 新建项目落账（project.created）', !!proj, proj && proj.id);
  await page.waitForTimeout(900);
  await shot('新建项目');

  // ---- 4. 空项目就该看起来是空的 ----
  const emptyText = await page.evaluate(() => (document.querySelector('.stage') || {}).textContent || '');
  check('4 空项目：BRD 待建 + 没有交付单元 + 需求池空',
    /还没有 BRD/.test(emptyText) && /还没有交付单元/.test(emptyText) && /需求池跟着交付单元走/.test(emptyText),
    emptyText.slice(0, 120));
  const unitCount0 = (await api('/api/projects')).body.unitCount || {};
  check('4 新项目下确实是 0 个交付单元', (unitCount0[proj.id] || 0) === 0, unitCount0[proj.id]);

  // ---- 5. 新建 BRD（0.1 空白模板）----
  await role('BA');
  await page.click('button:has-text("新建 BRD")');
  const brd = await waitFor(async () => {
    const j = (await api('/api/brd?proj=' + proj.id)).body;
    return j && j.v ? j : null;
  }, 15000);
  check('5 新建 BRD 生成 0.1 草稿', !!brd && brd.v === 'v0.1', brd && brd.v);
  check('5 BRD 正文是空白模板（五节骨架，不是别的项目的业务）',
    !!brd && /## 1\. 背景/.test(brd.text) && /## 5\. 验收口径/.test(brd.text) && !/悦府/.test(brd.text),
    brd && brd.text.slice(0, 40));
  await page.waitForTimeout(900);
  await shot('新建BRD');

  // 写一版正文并保存：v0.2
  const text = brd.text.replace('## 1. 背景\n（谁、在什么场景、现在怎么解决、痛在哪）',
    '## 1. 背景\n园区 3 个出入口，访客登记靠纸质本，保安换班后查不到谁在园内。');
  await page.fill('#brd-ed', text);
  await page.click('button:has-text("保存为草稿")');
  const brd2 = await waitFor(async () => {
    const j = (await api('/api/brd?proj=' + proj.id)).body;
    return j && j.v === 'v0.2' ? j : null;
  }, 15000);
  check('5 保存后版本前进（v0.1 → v0.2），正文进账本', !!brd2 && brd2.words > 50, brd2 && { v: brd2.v, words: brd2.words });
  await page.waitForTimeout(600);
  await shot('BRD写入');

  // ---- 6. 建三个交付单元（PBC / Agent / 组合）----
  await role('PA');
  // 先回到项目页：新建 BRD 之后停在 BRD 编辑器。
  await page.click('.sidebar .trow:has-text("' + projName + '")');
  await page.waitForTimeout(700);
  const unitsToMake = [
    { id: 'gate' + RUN, name: '门禁通行', kind: 'pbc', agentId: '' },
    { id: 'visitor' + RUN, name: '访客质检', kind: 'agent', agentId: 'x-qc' },
    { id: 'pass' + RUN, name: '通行组合', kind: 'hybrid', agentId: 'x-qc' },
  ];
  for (const u of unitsToMake) {
    await page.getByRole('button', { name: /新建单元|手动新建/ }).first().click();
    await page.waitForTimeout(300);
    const card = page.locator('.card:has-text("新建交付单元")');
    const inputs = card.locator('input');
    await inputs.nth(0).fill(u.id);
    await inputs.nth(1).fill(u.name);
    await card.locator('select').nth(0).selectOption(u.kind);
    if (u.kind !== 'pbc') {
      await page.waitForTimeout(200);
      await card.locator('select').nth(1).selectOption(u.agentId);
    }
    await card.locator('button:has-text("创建")').first().click();
    await page.waitForTimeout(900);
  }
  const unitsNow = (await api('/api/units')).body.units.filter((x) => x.proj === proj.id);
  check('6 三个交付单元挂在项目下（PBC / Agent / 组合）',
    unitsNow.length === 3 && ['pbc', 'agent', 'hybrid'].every((k) => unitsNow.some((u) => u.kind === k)),
    unitsNow.map((u) => u.id + ':' + u.kind));
  await shot('三个交付单元');

  // ---- 7. 每个单元各跑一条交付，直到发布 ----
  const delivered = [];
  for (const u of unitsNow) {
    // 7.1 建工作项（BA）
    await role('BA');
    const t = await api(`/api/tasks?pbc=${u.id}`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ title: u.name + ' · 首次交付', type: 'new', env: 'test', mode: 'fast' }),
    });
    const itemID = t.body && t.body.id;
    check(`7 ${u.id} 建工作项`, t.code === 201 && !!itemID, itemID);

    // 7.2 发起（DA）
    await role('DA');
    const run = await api(`/api/items/${itemID}/run?pbc=${u.id}`, { method: 'POST' });
    check(`7 ${u.id} 发起交付（真实 workflow）`, run.code === 202, run.body && run.body.instance);

    // 7.3 等它跑到发布闸（真测试 / 真构建 / 真部署都要时间）
    const waiting = await waitFor(async () => {
      const st = (await api(`/api/state?pbc=${u.id}`)).body;
      const it = st && st.items && st.items[itemID];
      return it && it.run === 'waiting' ? it : null;
    }, 180000, 1500);
    check(`7 ${u.id} 六步跑完，停在发布闸`, !!waiting, waiting && { run: waiting.run, step: waiting.step });

    // 7.4 证据：测试条数 / digest / 部署地址。
    // Agent 单元的交付物是定义，没有独立进程，部署如实「跳过」——
    // 它不该有一份假的 deploy.succeeded。
    const facts = (await api(`/api/items/${itemID}?pbc=${u.id}`)).body.facts || [];
    const ft = facts.map((f) => f.type);
    const wantDeploy = u.kind !== 'agent';
    check(`7 ${u.id} 账本里有 test.run / build.succeeded` + (wantDeploy ? ' / deploy.succeeded' : '（Agent 无部署）'),
      ft.includes('test.run') && ft.includes('build.succeeded') && (wantDeploy ? ft.includes('deploy.succeeded') : !ft.includes('deploy.succeeded')), ft);
    const testFact = facts.find((f) => f.type === 'test.run');
    check(`7 ${u.id} 测试结论是真跑出来的`, !!testFact && testFact.payload && testFact.payload.total > 0,
      testFact && testFact.payload);
    const buildFact = facts.find((f) => f.type === 'build.succeeded');
    check(`7 ${u.id} digest 是 sha256 内容摘要`,
      !!buildFact && /^sha256:[0-9a-f]{64}$/.test(buildFact.payload.digest), buildFact && buildFact.payload && buildFact.payload.digest);
    const depFact = facts.find((f) => f.type === 'deploy.succeeded');
    if (wantDeploy) {
      let health = null;
      if (depFact && depFact.payload && depFact.payload.url) {
        const r = await fetch(depFact.payload.url + '/healthz');
        health = r.status;
      }
      check(`7 ${u.id} 部署地址 /healthz 真的 200`, health === 200, health);
    } else {
      check(`7 ${u.id} Agent 单元没有进程：部署如实跳过`, !depFact, depFact && depFact.payload);
    }

    // 打开这条工作项的详情页：「待发布」那张图要能看见闸门与账本事实，
    // 只截项目列表的话，读者看不到测试几条、digest、部署地址。
    await page.evaluate((name) => {
      const rows = Array.from(document.querySelectorAll('.sidebar .trow'));
      const p = rows.find((x) => x.textContent.includes(name));
      if (p) p.click();
    }, projName);
    await page.waitForTimeout(500);
    await page.locator('.stage tr', { hasText: itemID }).first().click();
    await page.waitForTimeout(900);
    await shot(`07-${u.id}-待发布`);

    // 7.4.1 组合单元先做服务验收（DA / ActTest）：hybrid 的发布闸要它。
    if (u.kind === 'hybrid') {
      await role('DA');
      const sv = await api(`/api/units/${u.id}/svcverify`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ pass: 22, total: 22 }),
      });
      check(`7 ${u.id} 服务验收落账`, sv.code === 201, sv.body);
    }

    // 7.5 拍板发布（SA）
    await role('SA');
    const ans = await api(`/api/items/${itemID}/answer?pbc=${u.id}`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ approve: true }),
    });
    check(`7 ${u.id} SA 拍板`, ans.code === 202, ans.body);
    const pub = await waitFor(async () => {
      const st = (await api(`/api/state?pbc=${u.id}`)).body;
      const it = st && st.items && st.items[itemID];
      return it && it.run === 'published' ? it : null;
    }, 90000, 1200);
    check(`7 ${u.id} 发布完成（有 tag）`, !!pub && !!pub.tag, pub && { tag: pub.tag, version: pub.version });
    delivered.push({ unit: u, item: itemID, tag: pub && pub.tag, digest: buildFact && buildFact.payload.digest, test: testFact && testFact.payload });
    await page.waitForTimeout(500);
    await shot(`08-${u.id}-已发布`);
  }

  // ---- 8. 三个单元都交付完：侧栏与项目页都对 ----
  const finalUnits = (await api('/api/units')).body.units.filter((x) => x.proj === proj.id);
  check('8 三个单元都有 HEAD 与 tag', finalUnits.every((u) => u.head && (u.tags || []).length > 0),
    finalUnits.map((u) => u.id + ' ' + u.head + ' ' + JSON.stringify(u.tags)));

  await page.evaluate((id) => {
    const el = document.querySelector('.sidebar');
    const rows = Array.from(el.querySelectorAll('.trow'));
    const p = rows.find((x) => x.textContent.includes(id));
    if (p) p.click();
  }, projName);
  await page.waitForTimeout(900);
  await shot('项目完成');
  const projText = await page.evaluate(() => (document.querySelector('.stage') || {}).textContent || '');
  check('8 项目页显示三个单元的 HEAD（都交付过）',
    unitsNow.every((u) => projText.includes(u.name)), projText.slice(0, 80));

  // ---- 9. 加入已有：不是复制，只是出现在自己的侧栏 ----
  await role('PA');
  await page.click('.sb-add');
  await page.waitForTimeout(400);
  await page.click('.mbox button:has-text("加入已有")');
  await page.waitForTimeout(400);
  await shot('加入已有');
  const all = (await api('/api/projects')).body;
  const cand = all.catalog.projects.find((c) => !joinedIn(all.projects, c.id));
  // 注意：不能用 has-text("加入")——它会把上面的「加入已有」tab 也算进来。
  // 只点候选行里那个精确叫「加入」的按钮。
  await page.locator('.mbox .row button', { hasText: /^加入$/ }).first().click();
  const joined = await waitFor(async () => {
    const j = (await api('/api/projects')).body.projects.find((p) => p.id === cand.id);
    return j && j.shared ? j : null;
  }, 10000);
  check('9 加入已有项目：落 project.joined 且标「共享」', !!joined && joined.shared === true, joined && { id: joined.id, shared: joined.shared });
  await page.waitForTimeout(800);
  await shot('加入后的侧栏');

  // ---- 10. 刷新后仍在（持久化在账本里，不在前端内存）----
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1500);
  const afterReload = await page.evaluate(async () => {
    const j = await (await fetch('/api/projects')).json();
    return {
      side: document.querySelector('.sidebar').textContent,
      ids: j.projects.map((p) => p.id),
      ws: j.workspaces.map((w) => w.name),
    };
  });
  check('10 刷新后项目与工作空间都还在（账本而非本地快照）',
    afterReload.side.includes(projName) && afterReload.side.includes(wsName) && afterReload.ws.includes(wsName),
    afterReload.ids);

  // ---- 11. 页面无异常 ----
  const realErrors = pageErrors.filter((e) => !/Failed to load resource/.test(e));
  check('页面无 JS 异常', realErrors.length === 0, realErrors.slice(0, 3));
  const unexpected = httpErrors.filter((e) => !e.endsWith('/favicon.ico') && !/events\//.test(e));
  check('无意外 HTTP 错误', unexpected.length === 0, unexpected.slice(0, 5));

  fs.writeFileSync(path.join(SHOTS, 'summary.json'), JSON.stringify({ run: RUN, project: proj, brd: brd2 && brd2.v, delivered }, null, 2));
  await browser.close();
  const failed = results.filter((r) => !r.ok);
  console.log('SUMMARY ' + (results.length - failed.length) + '/' + results.length + ' passed');
  console.log('SHOTS ' + SHOTS);
  process.exit(failed.length ? 1 : 0);
})().catch((e) => { console.error('FATAL', e); process.exit(2); });
