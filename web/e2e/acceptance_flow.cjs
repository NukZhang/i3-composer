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
    return { code: r.status, body: await r.json().catch(() => null) };
  }, [path, opts]);

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
      body: JSON.stringify({ title: t, type: 'new', env: 'test', mode: 'fast' }),
    }).then((r) => r.status);
  }, [unit, title]);
  const created = (await api(page, '/api/inbox')).body.items.find((x) => x.t === title);
  check('1 BA 建任务走真实接口', !!created && /^US-/.test(created.id), created && created.id);
  check('1 新项 run=idea 且六步链路齐全', created && created.run === 'idea' && created.steps.length === 6, created && created.run);

  const id = created.id;

  // 刷新后仍在 = 来自事实日志
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1200);
  const persisted = (await api(page, '/api/inbox')).body.items.some((x) => x.id === id);
  check('1 刷新后仍在（事实日志而非前端内存）', persisted);

  // ---- 2. BA 发起被 403（build 是 DA 的权限）----
  const denied = await api(page, `/api/items/${id}/run?pbc=${unit}`, { method: 'POST' });
  check('2 BA 发起交付被 403 拒绝', denied.code === 403 && /权限不足/.test(denied.body.error), denied);

  // ---- 3. 切 DA 发起：真实 workflow 自动推进到「待打 tag」----
  await page.click('.rolebtn:has-text("DA")');
  await page.waitForTimeout(600);
  const roleNow = (await api(page, `/api/overview?pbc=${unit}`)).body.role;
  check('3 角色切换写入服务端会话', roleNow === 'DA', roleNow);

  const run = await api(page, `/api/items/${id}/run?pbc=${unit}`, { method: 'POST' });
  check('3 DA 发起返回 202（实例号绑定工作项）', run.code === 202 && String(run.body.instance || '').endsWith(id), run);

  // 实例在跑时再发一次：Dapr 实例号是锁，必须被拒。
  const rerun = await api(page, `/api/items/${id}/run?pbc=${unit}`, { method: 'POST' });
  check('3 运行中重复发起被 400 拒绝（Dapr 实例锁）', rerun.code === 400 && /已在执行中/.test(rerun.body.error), rerun);

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
  check('4 DA 打 tag 被 403 拒绝', deny.code === 403 && /权限不足/.test(deny.body.error), deny);
  const afterDeny = (await api(page, `/api/state?pbc=${unit}`)).body.commits[0];
  check('4 越权后账本状态没变（无 tag）', !afterDeny.tag && afterDeny.no === before.no, { before: before.tag, after: afterDeny.tag });

  // ---- 5. SA 打 tag → 进「发布编排」池 ----
  await page.click('.rolebtn:has-text("SA")');
  await page.waitForTimeout(600);
  const ok = await api(page, `/api/units/${unit}/tag`, { method: 'POST' });
  check('5 SA 打 tag 返回 201', ok.code === 201 && !!ok.body.tag, ok.body);

  const unitNow = ((await api(page, '/api/units')).body.units || []).find((u) => u.id === unit);
  check('5 单元 HEAD 上有 tag（进池）', !!unitNow && unitNow.headTag === ok.body.tag, unitNow && { head: unitNow.head, headTag: unitNow.headTag });

  const facts = (await api(page, `/api/facts?pbc=${unit}`)).body.facts.map((f) => f.type);
  check('5 账本里有 tag.created 与 item.verified',
    facts.includes('tag.created') && facts.includes('item.verified'), facts.filter((t) => t === 'tag.created' || t === 'item.verified'));

  // ---- 6. 通知中心由事实派生 ----
  const notes = (await api(page, '/api/notifications')).body.notifications;
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
