// 写路径验收：建任务 → 发起（真实 workflow + SSE 自动推进）→ 越权被拒 → SA 拍板 → 发布。
const { chromium } = require('playwright');
const CHROME = '/Users/nukzhang/Library/Caches/ms-playwright/chromium-1217/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing';

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

  const BASE = process.env.BASE || 'http://127.0.0.1:8092';
  await page.goto(BASE + '/', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1200);

  // 切到 BA（默认就是 BA，但显式切一次，确保会话 cookie 存在）
  await page.click('.rolebtn:has-text("BA")');
  await page.waitForTimeout(500);

  // ---- 1. 建任务（BA / backlog）----
  await page.click('.sidebar .trow:has-text("悦府物业")');
  await page.waitForTimeout(600);
  const title = '端到端验收-' + Date.now();
  await page.fill('#cr-title', title);
  await page.click('.cr-form button.primary');
  await page.waitForTimeout(1200);
  const created = (await api(page, '/api/inbox')).body.items.find((x) => x.t === title);
  check('1 BA 建任务走真实接口', !!created && /^US-/.test(created.id), created && created.id);
  check('1 新项 run=idea 且七步链路齐全', created && created.run === 'idea' && created.steps.length === 7, created && created.run);
  const toast = await page.evaluate(() => Array.from(document.querySelectorAll('.tst')).map((d) => d.textContent).join(' | '));
  check('1 页面 toast 显示创建结果', /已创建/.test(toast), toast);

  const id = created.id;

  // 刷新后仍在 = 来自事实日志
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1200);
  const persisted = (await api(page, '/api/inbox')).body.items.some((x) => x.id === id);
  check('1 刷新后仍在（事实日志而非前端内存）', persisted);

  // ---- 2. BA 发起被 403（build 是 DA 的权限）----
  const denied = await api(page, `/api/items/${id}/run?pbc=fee`, { method: 'POST' });
  check('2 BA 发起交付被 403 拒绝', denied.code === 403 && /权限不足/.test(denied.body.error), denied);

  // ---- 3. 切 DA 发起：真实 workflow 自动推进到发布闸 ----
  await page.click('.rolebtn:has-text("DA")');
  await page.waitForTimeout(600);
  const roleNow = (await api(page, '/api/overview?pbc=fee')).body.role;
  check('3 角色切换写入服务端会话', roleNow === 'DA', roleNow);

  const run = await api(page, `/api/items/${id}/run?pbc=fee`, { method: 'POST' });
  // 实例号在默认账本上就是工作项号；隔离账本会加前缀（避免两套账本抢同一个实例）。
  check('3 DA 发起返回 202（实例号绑定工作项）', run.code === 202 && String(run.body.instance || '').endsWith(id), run);

  // 不手动刷新：靠 SSE 通知 + 重拉，页面应自动推进
  const uiWaiting = await waitFor(
    () => page.evaluate(async (i) => {
      const st = await fetch('/api/state?pbc=fee').then((r) => r.json());
      return st.items[i] && st.items[i].run === 'waiting' ? st.items[i] : null;
    }, id),
    90000, 1000,
  );
  check('3 页面自动推进到 run=waiting（SSE 驱动，无需手动刷新）', !!uiWaiting, uiWaiting && { run: uiWaiting.run, step: uiWaiting.step });
  check('3 第七步是人工闸且未执行', uiWaiting && uiWaiting.step === 6 && uiWaiting.steps[6].st === 'todo' && uiWaiting.steps[6].human === true, uiWaiting && uiWaiting.steps[6]);
  check('3 前六步全部完成', uiWaiting && [0, 1, 2, 3, 4, 5].every((i) => uiWaiting.steps[i].st === 'done'), uiWaiting && uiWaiting.steps.map((s) => s.st));

  const rerun = await api(page, `/api/items/${id}/run?pbc=fee`, { method: 'POST' });
  check('3 重复发起被 400 拒绝（Dapr 实例锁）', rerun.code === 400 && /已在执行中/.test(rerun.body.error), rerun);

  // ---- 4. DA 拍板被 403：界面不能假装成功 ----
  const before = (await api(page, `/api/state?pbc=fee`)).body.items[id];
  const deny = await api(page, `/api/items/${id}/answer?pbc=fee`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ approve: true }),
  });
  check('4 DA 拍板被 403 拒绝', deny.code === 403 && /权限不足/.test(deny.body.error), deny);
  const afterDeny = (await api(page, `/api/state?pbc=fee`)).body.items[id];
  check('4 越权后账本状态没变（无乐观更新）', afterDeny.run === before.run && afterDeny.run === 'waiting', { before: before.run, after: afterDeny.run });

  // ---- 5. SA 拍板 → workflow 打 tag → 发布 ----
  await page.click('.rolebtn:has-text("SA")');
  await page.waitForTimeout(600);
  const ok = await api(page, `/api/items/${id}/answer?pbc=fee`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ approve: true }),
  });
  check('5 SA 拍板返回 202', ok.code === 202 && ok.body.approve === true, ok);

  const published = await waitFor(
    () => page.evaluate(async (i) => {
      const st = await fetch('/api/state?pbc=fee').then((r) => r.json());
      return st.items[i] && st.items[i].run === 'published' ? st.items[i] : null;
    }, id),
    90000, 1000,
  );
  check('5 几秒内 run 变 published', !!published, published && published.run);
  check('5 显示了 tag 与版本', !!published && !!published.tag && !!published.version, published && { tag: published.tag, version: published.version });

  const facts = (await api(page, `/api/items/${id}?pbc=fee`)).body.facts.map((f) => f.type);
  check('5 账本里有 publish.decided 与 item.published',
    facts.includes('publish.decided') && facts.includes('item.published'), facts);

  // ---- 6. 通知中心由事实派生 ----
  const notes = (await api(page, '/api/notifications')).body.notifications;
  check('6 通知中心出现该工作项的发布事件', notes.some((n) => n.text.includes(id)), notes.slice(0, 2));

  // 注意：本脚本自己用 fetch 制造了 403 / 400（越权与重复发起），
  // 浏览器把它们记为资源加载错误；那不是页面的 JS 异常。
  const realErrors = pageErrors.filter((e) => !/Failed to load resource/.test(e));
  check('页面无 JS 异常', realErrors.length === 0, realErrors.slice(0, 3));
  const unexpected = httpErrors.filter(
    (e) => !e.endsWith('/favicon.ico') && !e.startsWith('403 ') && !/\/api\/items\/US-\d+\/run/.test(e),
  );
  check('除脚本自造的 403/400 外无 HTTP 错误', unexpected.length === 0, unexpected.slice(0, 5));

  await browser.close();
  const failed = results.filter((r) => !r.ok);
  console.log('SUMMARY ' + (results.length - failed.length) + '/' + results.length + ' passed');
  process.exit(failed.length ? 1 : 0);
})().catch((e) => { console.error('FATAL', e); process.exit(2); });
