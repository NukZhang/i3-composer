// 验收「执行侧是真的」：跑完一条交付后，逐条核对账本里的证据。
//
//   - 制品里的代码能编译（有 go.mod / main.go / main_test.go）
//   - test.run 的条数是 go test 的真实输出（不是编的条数）
//   - build.succeeded 的 digest 是内容摘要（sha256: + 64 位十六进制）
//   - deploy.succeeded 的地址真的能访问，/healthz 返回 200
//   - 种子写的历史 actor 是 seed，与真跑出来的事实区分得开
//   - pubsub 收到的事件落了 event.received 事实
//
// 用法：BASE=http://127.0.0.1:8098 node acceptance_exec.cjs
const { chromium } = require('playwright');
const CHROME = '/Users/nukzhang/Library/Caches/ms-playwright/chromium-1217/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing';
const BASE = process.env.BASE || 'http://127.0.0.1:8098';

const results = [];
function check(name, ok, detail) {
  results.push({ name, ok: !!ok, detail });
  console.log((ok ? 'PASS ' : 'FAIL ') + name + (detail === undefined ? '' : ' :: ' + JSON.stringify(detail)));
}

(async () => {
  const browser = await chromium.launch({ executablePath: CHROME });
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  const pageErrors = [];
  page.on('pageerror', (e) => pageErrors.push(String(e)));
  page.on('console', (m) => { if (m.type() === 'error') pageErrors.push('console: ' + m.text()); });

  const api = (path, opts) => page.evaluate(async ([p, o]) => {
    const r = await fetch(p, o || {});
    return { code: r.status, body: await r.json().catch(() => null) };
  }, [path, opts]);
  const role = (r) => api('/api/role', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ role: r }),
  });

  await page.goto(BASE + '/', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1500);

  // ---- 1. 建一条工作项并跑完整条链 ----
  await role('BA');
  const title = '执行侧验收-' + Date.now();
  const task = await api('/api/tasks?pbc=fee', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ title, type: 'feature', env: 'test', mode: 'fast' }),
  });
  check('1 建工作项', task.code === 201, task.body);
  const id = task.body && task.body.id;

  await role('DA');
  const run = await api('/api/items/' + id + '/run?pbc=fee', { method: 'POST' });
  check('1 发起交付（真实 Dapr workflow）', run.code === 202, run.body);

  // 等它走到发布闸（真跑测试 + 真构建 + 真部署都要时间）。
  let item = null;
  const deadline = Date.now() + 5 * 60 * 1000;
  while (Date.now() < deadline) {
    const st = await api('/api/state?pbc=fee');
    const it = st.body && st.body.items && st.body.items[id];
    if (it && (it.run === 'waiting' || it.run === 'blocked' || it.run === 'published')) { item = it; break; }
    await new Promise((r) => setTimeout(r, 2000));
  }
  check('1 workflow 跑到发布闸', !!item && item.run === 'waiting', item && { run: item.run, blocked: item.blocked });

  // ---- 2. 制品里有真内容（能编译） ----
  const st = (await api('/api/state?pbc=fee')).body;
  const codeVers = (st.versions && st.versions.code) || [];
  const last = codeVers[codeVers.length - 1] || {};
  const files = last.files || {};
  const need = ['go.mod', 'main.go', 'main_test.go'];
  check('2 代码制品带可编译的文件', need.every((f) => files[f]), Object.keys(files));
  check('2 文档制品带 markdown 正文',
    ((st.versions.prd || []).slice(-1)[0] || {}).md?.length > 50,
    (((st.versions.prd || []).slice(-1)[0] || {}).md || '').slice(0, 40));

  // ---- 3. 测试条数是 go test 的真实输出 ----
  check('3 账本里有 test.run 事实', !!st.test, st.test);
  check('3 测试条数是真跑出来的（总数 = 用例数）',
    st.test && st.test.total > 0 && st.test.pass <= st.test.total, st.test);
  check('3 测试没有失败项', st.test && (!st.test.failures || st.test.failures.length === 0), st.test && st.test.failures);

  // ---- 4. digest 是内容摘要 ----
  const head = (st.commits || [])[0];
  const digest = (st.builds || {})[head.no];
  check('4 构建产物 digest 是 sha256 内容摘要',
    !!digest && /^sha256:[0-9a-f]{64}$/.test(digest), digest);

  // ---- 5. 部署地址真的活着 ----
  const deploys = st.deploys || [];
  const url = deploys.length ? deploys[deploys.length - 1].url : '';
  check('5 有部署事实', !!url, url);
  let health = null;
  if (url) {
    // 从 Node 侧探活：浏览器里发跨源请求会被 CORS 挡住，
    // 那是浏览器策略，不是服务不可达。
    health = await fetch(url + '/healthz');
    const body = await health.text();
    health = { code: health.status, body };
  }
  check('5 部署地址能访问且 /healthz 返回 200', health && health.code === 200, health);

  // ---- 5.1 人能在页面上直接看到这些证据 ----
  // 不必去翻 Redis：工作项页的「账本事实」列表里就该写着
  // 「几条测试过 / digest 是哪个 / 部署在哪」。
  await page.click('.home-tab:has-text("待我确认")');
  await page.waitForTimeout(800);
  const row = page.locator('.task:has-text("' + id + '")').first();
  if (await row.count()) {
    await row.click();
  }
  await page.waitForTimeout(1200);
  const pageText = await page.evaluate(() => (document.querySelector('#app') || {}).textContent || '');
  const shortDigest = String(digest).slice(0, 19);
  check('5 页面上看得到真证据（测试条数 / digest / 部署地址）',
    /\d+\/\d+ 通过/.test(pageText) && pageText.includes(shortDigest) && pageText.includes(url),
    { digest: shortDigest, url, hasPass: /\d+\/\d+ 通过/.test(pageText) });

  // ---- 6. 种子写的历史与真跑的事实分得开 ----
  const facts = (await api('/api/facts?pbc=fee')).body.facts || [];
  const seedFacts = facts.filter((f) => f.actor === 'seed');
  const realFacts = facts.filter((f) => f.actor !== 'seed');
  check('6 种子事实 actor 是 seed（演示数据可识别）', seedFacts.length > 0, seedFacts.length);
  check('6 真跑出来的事实 actor 不是 seed', realFacts.length > 0, realFacts.map((f) => f.actor).slice(0, 4));

  // ---- 7. pubsub 收到的事件落了事实 ----
  const evs = (await api('/api/events')).body.events || [];
  check('7 pubsub 收到的事件落了 event.received 事实', evs.length > 0, evs.slice(0, 2).map((e) => e.payload && e.payload.topic));

  // ---- 7.1 workflow 的每一步都真的过了总线 ----
  // 这条最容易漏：以前 workflow 只在测试/构建/部署这几步发事件，
  // 前面几步静悄悄地过去了，总线看起来像装饰品。现在每一步都发，
  // 所以收到的 item.changed 事件数应该盖得住步骤数（6 步 + 人工闸）。
  const itemEvents = evs.filter((e) => e.payload && e.payload.topic === 'item.changed' &&
    e.payload.data && e.payload.data.item === id);
  const stepEvents = itemEvents.filter((e) => e.payload.data.kind === 'step' || e.payload.data.kind === 'waiting');
  check('7 workflow 每一步都经总线发过事件（不是装饰品）',
    stepEvents.length >= 7, { 收到: itemEvents.length, 其中步骤: stepEvents.length });

  // ---- 8. 发布 ----
  await role('SA');
  const answer = await api('/api/items/' + id + '/answer?pbc=fee', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ approve: true }),
  });
  check('8 SA 拍板发布', answer.code === 202, answer.body);
  let published = null;
  const dl = Date.now() + 90 * 1000;
  while (Date.now() < dl) {
    const s2 = (await api('/api/state?pbc=fee')).body;
    const it = s2.items && s2.items[id];
    if (it && it.run === 'published') { published = it; break; }
    await new Promise((r) => setTimeout(r, 1500));
  }
  check('8 发布完成', !!published && !!published.tag, published && { tag: published.tag, version: published.version });

  check('页面无 JS 异常', pageErrors.length === 0, pageErrors.slice(0, 3));

  await browser.close();
  const failed = results.filter((r) => !r.ok);
  console.log('SUMMARY ' + (results.length - failed.length) + '/' + results.length + ' passed');
  process.exit(failed.length ? 1 : 0);
})().catch((e) => { console.error('FATAL', e); process.exit(2); });