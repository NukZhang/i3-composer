// 写路径验收（v3.2）：建单元 → 建任务 → 发起（真实 workflow + SSE 自动推进）→
// 越权被拒 → SA 打 tag 进池。
//
// 权限边界不变：发起是 DA 的活，打 tag 是 SA 的活；
// 越权时账本不能有任何变化（不做乐观更新）。
const { chromium } = require('playwright');
const CHROME = '/Users/nukzhang/Library/Caches/ms-playwright/chromium-1217/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing';
const RUN = process.env.RUN_TAG || String(Date.now()).slice(-6);

const results = [];
function check(name, ok, detail) {
  results.push({ name, ok: !!ok, detail });
  console.log((ok ? 'PASS ' : 'FAIL ') + name + (detail === undefined ? '' : ' :: ' + JSON.stringify(detail)));
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
const api = (page, path, opts) =>
  page.evaluate(async ([p, o]) => {
    const r = await fetch(p, o || {});
    const text = await r.text();
    let body = null;
    try { body = JSON.parse(text); } catch { /* 非 JSON（空体 / HTML 错误页） */ }
    return { code: r.status, body, text: text.slice(0, 300) };
  }, [path, opts]);
// needCall 取一个必成功的接口：拿不到 2xx 就把「哪个接口、什么状态、返回了什么」
// 记成一条 FAIL，然后返回 null——调用方据此停在这一段，不让整轮塌成 TypeError（#56）。
const needCall = (res, what) => {
  if (res.code >= 200 && res.code < 300 && res.body) return res;
  check(what + '：接口可用', false, { code: res.code, body: res.text });
  return null;
};

// healthPre 先问 /api/health：连不上、以及「BFF 在但账本失联」（503 + degraded=dapr，见 #55）
// 都算前置不通，并且把 sidecar 的结论带出来——带着失联的账本跑整轮，每条 check 都会是假的。
async function healthPre(BASE) {
  let r;
  try { r = await fetch(BASE + '/api/health'); } catch (e) { return { ok: false, reason: '连不上，先起服务再跑' }; }
  let b = null;
  try { b = await r.json(); } catch { /* 非 JSON */ }
  const d = (b && b.dapr) || {};
  if (!r.ok || (b && b.ok === false) || d.ok === false) {
    return { ok: false, reason: 'BFF 在，但与账本失联（sidecar 连不上），先起 sidecar 再跑', code: r.status, dapr: d, health: b && { ok: b.ok } };
  }
  return { ok: true };
}

(async () => {
  const browser = await chromium.launch({ executablePath: CHROME });
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  const pageErrors = [];
  const httpErrors = [];
  page.on('pageerror', (e) => pageErrors.push(String(e)));
  page.on('console', (m) => { if (m.type() === 'error') pageErrors.push('console: ' + m.text()); });
  page.on('response', (r) => { if (r.status() >= 400) httpErrors.push(r.status() + ' ' + r.url()); });

  const BASE = process.env.BASE || 'http://127.0.0.1:8080';
  // 本服务起没起：连不上时别让页面落到 origin 的 SPA 兜底（那里 /api/* 返回的是 HTML，
  // 每种读都会被当成「接口可用」）。起不来就只报这一条，后面的 check 无从谈起（#56）。
  const up = await healthPre(BASE);
  if (!up.ok) {
    check('前置：BFF 可用（' + BASE + '）', false, up);
    await browser.close();
    console.log('SUMMARY 0/' + results.length + ' passed');
    process.exit(1);
  }
  await page.goto(BASE + '/', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1200);

  // ---- 0. 建一个干净单元（种子单元的验收清单还没全过，打不了 tag）----
  await page.click('.rolebtn:has-text("PA")');
  await page.waitForTimeout(500);
  const unit = 'flow' + RUN;
  const cu = await api(page, '/api/units', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id: unit, name: '写路径验收单元', kind: 'pbc', proj: 'p1' }),
  });
  check('0 建写路径验收单元', cu.code === 201, cu.body);

  // 切到 BA
  await page.click('.rolebtn:has-text("BA")');
  await page.waitForTimeout(500);

  // ---- 1. 建任务（BA / backlog）----
  // 侧栏是「工作空间 → 项目 → 单元」：先点项目行、再点单元行。
  await page.click('.sidebar .trow.sub:has-text("收费与工单数字化")');
  await page.waitForTimeout(600);
  await page.evaluate((name) => {
    const rows = Array.from(document.querySelectorAll('.sidebar .trow'));
    const p = rows.find((x) => x.textContent.trim().startsWith(name));
    if (p) p.click();
  }, '写路径验收单元');
  await page.waitForTimeout(500);
  const title = '写路径验收-' + Date.now();
  // 通过 UI 建单：切到项目的需求池表单（后端按 pbc 参数建）。
  await page.evaluate(([u, t]) => {
    // 需求池的输入框在当前单元页；直接调接口更稳，UI 文本另外断言。
    return fetch('/api/tasks?pbc=' + u, {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        title: t, type: 'new', env: 'test', mode: 'fast',
        // 入口准入要的硬输入：可断言的验收口径 / 数据范围 / 验收人 / 合规背责人。
        accept: ['当业主提交缴费单，则应生成幂等键且重复提交不重复入账'],
        data: ['fee.bill.amount'], acceptBy: 'BA 业务', complyBy: 'SA 系统',
      }),
    }).then((r) => r.status);
  }, [unit, title]);
  // 建任务失败就如实停在这一步：以前这里 created 为 null，下面每条 check
  // 都是 TypeError，整轮塌成 FATAL，前因反倒看不见了（#52）。
  const inbox = needCall(await api(page, '/api/inbox'), '读需求池');
  const created = inbox && (((inbox.body || {}).items) || []).find((x) => x.t === title);
  check('1 BA 建任务走真实接口', !!created && /^US-/.test(created.id), created && created.id);
  check('1 新项 run=idea 且六步链路齐全', !!created && created.run === 'idea' && created.steps.length === 6, created && created.run);
  if (!created) {
    check('后续 check 未执行', false, { reason: '建任务失败，后续没有对象可验', unit });
    await browser.close();
    const failed = results.filter((r) => !r.ok);
    console.log('SUMMARY ' + (results.length - failed.length) + '/' + results.length + ' passed');
    process.exit(failed.length ? 1 : 0);
  }

  const id = created.id;

  // 刷新后仍在 = 来自事实日志
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1200);
  const persisted = ((((await api(page, '/api/inbox')).body || {}).items) || []).some((x) => x.id === id);
  check('1 刷新后仍在（事实日志而非前端内存）', persisted);

  // ---- 2. BA 发起被 403（build 是 DA 的权限）----
  const denied = await api(page, `/api/items/${id}/run?pbc=${unit}`, { method: 'POST' });
  check('2 BA 发起交付被 403 拒绝', denied.code === 403 && /权限不足/.test((denied.body || {}).error), denied);

  // ---- 2b. 入口准入：只写标题的项，DA 发起被 400 挡在门口，且账本不留 run.started ----
  const bare = await api(page, `/api/tasks?pbc=${unit}`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ title: '只写了标题-' + Date.now(), type: 'feature', env: 'test', mode: 'fast' }),
  });
  const bareID = bare.body && bare.body.id;
  check('2b 建一条只有标题的工作项', bare.code === 201 && !!bareID, bareID);
  await page.click('.rolebtn:has-text("DA")');
  await page.waitForTimeout(400);
  const bareRun = await api(page, `/api/items/${bareID}/run?pbc=${unit}`, { method: 'POST' });
  check('2b 准入不过的发起被 400 拒绝，并点名缺什么、找谁',
    bareRun.code === 400 && /目标可断言/.test((bareRun.body || {}).error) && /数据与权限/.test((bareRun.body || {}).error) && /责任人/.test((bareRun.body || {}).error), bareRun.body && bareRun.body.error);
  const bareFacts = ((((await api(page, `/api/facts?pbc=${unit}`)).body || {}).facts) || []).map((f) => f.type);
  check('2b 被拒的发起不落 item.run.started，只留 item.admit.rejected',
    !bareFacts.includes('item.run.started') && bareFacts.includes('item.admit.rejected'), bareFacts.filter((t) => /admit|run.started/.test(t)));

  // ---- 2c. 页面上把「缺什么 / 找谁 / 补哪」摊开，并就地补输入 ----
  // 被拒不是一个 toast 就完事：从下面几条理由里能直接读出要补什么、找谁要。
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1200);
  await page.click('.rolebtn:has-text("BA")');
  await page.waitForTimeout(500);
  // 项目页的需求池列的是本项目所有单元的工作项，点项目行就够了。
  await page.click('.sidebar .trow.sub:has-text("收费与工单数字化")');
  await page.waitForTimeout(900);
  const rowHit = await page.evaluate((i) => {
    const row = Array.from(document.querySelectorAll('tbody tr')).find((x) => x.textContent.includes(i));
    if (row) row.click();
    return !!row;
  }, bareID);
  check('2c 被拒的工作项仍在需求池里（说明进过账本）', rowHit, bareID);
  await page.waitForTimeout(900);
  const gateText = await page.evaluate(() => {
    const g = document.querySelector('.gate');
    return g ? g.textContent : '';
  });
  check('2c 被拒的工作项页面点名缺什么、找谁、补哪',
    /目标可断言/.test(gateText) && /找 BA 业务/.test(gateText) && /补一句验收口径/.test(gateText)
      && /数据与权限/.test(gateText) && /责任人/.test(gateText), gateText.slice(0, 160));

  await page.click('.rolebtn:has-text("BA")');
  await page.waitForTimeout(500);
  const fixBtn = await page.evaluate(() => {
    const b = Array.from(document.querySelectorAll('.gate button')).find((x) => x.textContent.includes('补输入'));
    if (b) b.click();
    return !!b;
  });
  check('2c BA 有「补输入」入口（DA 只读，按钮不给）', fixBtn);
  await page.waitForTimeout(300);
  await page.fill('.gate .admit-fix textarea', '当业主提交缴费单，则应生成幂等键且重复提交不重复入账');
  const fixInputs = await page.$$('.gate .admit-fix input');
  await fixInputs[0].fill('fee.bill.amount');
  await fixInputs[1].fill('BA 业务');
  await fixInputs[2].fill('SA 系统');
  await page.evaluate(() => {
    const b = Array.from(document.querySelectorAll('.admit-fix button')).find((x) => x.textContent.includes('补进账本'));
    if (b) b.click();
  });
  const fixed = await waitFor(async () => {
    const f = ((((await api(page, `/api/facts?pbc=${unit}`)).body || {}).facts) || []).map((x) => x.type);
    return f.includes('item.admit.inputs') ? f : null;
  }, 8000, 500);
  check('2c 补的四项落成事实 item.admit.inputs', !!fixed, fixed && fixed.filter((t) => t === 'item.admit.inputs'));

  // 硬项补齐后发起：放行，但两个软项没补 → 账本记 waiver，页面挂黄条
  await page.click('.rolebtn:has-text("DA")');
  await page.waitForTimeout(500);
  const nowRun = await api(page, `/api/items/${bareID}/run?pbc=${unit}`, { method: 'POST' });
  check('2c 硬项补齐后 DA 发起 202（不再被准入挡）', nowRun.code === 202, nowRun);
  const admitted = await api(page, `/api/items/${bareID}?pbc=${unit}`);
  const admit = ((admitted.body || {}).item || {}).admit;
  check('2c 放行带两条 waiver（软项没补，但不挡发起）',
    !!admit && admit.st === 'admitted' && (admit.waivers || []).length === 2, admit);
  const waiveText = await waitFor(() => page.evaluate(() => {
    const w = document.querySelector('.ws.warn');
    return w ? w.textContent : null;
  }), 15000, 800);
  check('2c 页面黄条列出 waived 的两项', !!waiveText && /范围边界/.test(waiveText) && /上下文齐备/.test(waiveText), waiveText && waiveText.slice(0, 120));

  // ---- 3. 切 DA 发起：真实 workflow 自动推进到「待打 tag」----
  await page.click('.rolebtn:has-text("DA")');
  await page.waitForTimeout(600);
  const roleNow = (await api(page, `/api/overview?pbc=${unit}`)).body.role;
  check('3 角色切换写入服务端会话', roleNow === 'DA', roleNow);

  const run = await api(page, `/api/items/${id}/run?pbc=${unit}`, { method: 'POST' });
  check('3 DA 发起返回 202（实例号绑定工作项）', run.code === 202 && String(run.body.instance || '').endsWith(id), run);

  // 实例在跑时再发一次：Dapr 实例号是锁，必须被拒。
  const rerun = await api(page, `/api/items/${id}/run?pbc=${unit}`, { method: 'POST' });
  check('3 运行中重复发起被 400 拒绝（Dapr 实例锁）', rerun.code === 400 && /已在执行中/.test((rerun.body || {}).error), rerun);

  // 不手动刷新：靠 SSE 通知 + 重拉，页面应自动推进
  const uiVerified = await waitFor(
    () => page.evaluate(async ([u, i]) => {
      const st = await fetch('/api/state?pbc=' + u).then((r) => r.json());
      return st.items[i] && st.items[i].run === 'verified' ? st.items[i] : null;
    }, [unit, id]),
    90000, 1000,
  );
  check('3 页面自动推进到 run=verified（SSE 驱动，无需手动刷新）', !!uiVerified, uiVerified && { run: uiVerified.run, step: uiVerified.step });
  check('3 第六步是人工闸且未执行', uiVerified && uiVerified.step === 5 && uiVerified.steps[5].st === 'todo' && uiVerified.steps[5].human === true, uiVerified && uiVerified.steps[5]);
  check('3 前五步全部完成', uiVerified && [0, 1, 2, 3, 4].every((i) => uiVerified.steps[i].st === 'done'), uiVerified && uiVerified.steps.map((s) => s.st));


  // ---- 4. DA 打 tag 被 403：界面不能假装成功 ----
  const before = (await api(page, `/api/state?pbc=${unit}`)).body.commits[0];
  const deny = await api(page, `/api/units/${unit}/tag`, { method: 'POST' });
  check('4 DA 打 tag 被 403 拒绝', deny.code === 403 && /权限不足/.test((deny.body || {}).error), deny);
  const afterDeny = (await api(page, `/api/state?pbc=${unit}`)).body.commits[0];
  check('4 越权后账本状态没变（无 tag）', !afterDeny.tag && afterDeny.no === before.no, { before: before.tag, after: afterDeny.tag });

  // ---- 5. SA 打 tag → 进「发布编排」池 ----
  await page.click('.rolebtn:has-text("SA")');
  await page.waitForTimeout(600);
  const ok = await api(page, `/api/units/${unit}/tag`, { method: 'POST' });
  check('5 SA 打 tag 返回 201', ok.code === 201 && !!(ok.body && ok.body.tag), ok.body);

  const unitNow = (((await api(page, '/api/units')).body || {}).units || []).find((u) => u.id === unit);
  check('5 单元 HEAD 上有 tag（进池）',
    !!unitNow && !!ok.body && unitNow.headTag === ok.body.tag, unitNow && { head: unitNow.head, headTag: unitNow.headTag });

  const facts = ((((await api(page, `/api/facts?pbc=${unit}`)).body || {}).facts) || []).map((f) => f.type);
  check('5 账本里有 tag.created 与 item.verified',
    facts.includes('tag.created') && facts.includes('item.verified'), facts.filter((t) => t === 'tag.created' || t === 'item.verified'));

  // ---- 6. 通知中心由事实派生 ----
  const notes = ((await api(page, '/api/notifications')).body || {}).notifications || [];
  check('6 通知中心出现该单元的 tag 事件', notes.some((n) => n.type === 'tag.created' && n.unit === unit), notes.slice(0, 2));

  // 注意：本脚本自己用 fetch 制造了 403 / 400（越权与重复发起），
  // 浏览器把它们记为资源加载错误；那不是页面的 JS 异常。
  const realErrors = pageErrors.filter((e) => !/Failed to load resource/.test(e));
  check('页面无 JS 异常', realErrors.length === 0, realErrors.slice(0, 3));
  const unexpected = httpErrors.filter(
    (e) => !e.endsWith('/favicon.ico') && !e.startsWith('403 ') && !/\/api\/items\/US-\d+\/run/.test(e) && !/\/api\/units/.test(e),
  );
  check('除脚本自造的 403/400 外无 HTTP 错误', unexpected.length === 0, unexpected.slice(0, 5));

  await browser.close();
  const failed = results.filter((r) => !r.ok);
  console.log('SUMMARY ' + (results.length - failed.length) + '/' + results.length + ' passed');
  process.exit(failed.length ? 1 : 0);
})().catch((e) => { console.error('FATAL', e); process.exit(2); });
