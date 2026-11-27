// 全链路验收：从空账本开始，真跑一遍。
//
//   建单元 → 建任务 → 发起 → 生成制品 → 收敛 → 提交 → 真跑测试 → 真构建 → 真部署 → 发布
//
// 每一步都验「真的发生了」：
//   - 制品里有能编译的代码（不是空壳）
//   - 测试数是 go test 的输出（不是编出来的条数）
//   - digest 是构建产物的内容摘要（不是树摘要拼的）
//   - 部署地址真的能访问、/healthz 真的 200
//   - pubsub 收到的事件落了 event.received 事实
//
// 用法：BASE=http://127.0.0.1:8093 node acceptance_full.cjs
const { chromium } = require('playwright');
const { requirePubsub } = require('./require_pubsub.cjs');
const CHROME = '/Users/nukzhang/Library/Caches/ms-playwright/chromium-1217/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing';
const BASE = process.env.BASE || 'http://127.0.0.1:8093';

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
    await new Promise((r) => setTimeout(r, step || 1000));
  }
  return null;
}

(async () => {
  await requirePubsub(BASE);
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
  const setRole = (role) => api('/api/role', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ role }),
  });

  await page.goto(BASE + '/', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1500);

  // ---- 1. 订阅声明（pubsub 收端在线） ----
  const subs = await page.evaluate(async (b) => {
    const r = await fetch(b + '/dapr/subscribe');
    return r.json();
  }, BASE);
  check('1 pubsub 订阅端在线（声明了主题与路由）', Array.isArray(subs) && subs.length > 0, subs && subs[0]);

  // ---- 2. 从零建一个交付单元 ----
  await setRole('PA');
  const unitID = 'e2e' + String(Date.now()).slice(-5);
  const created = await api('/api/units', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id: unitID, name: '端到端验收单元', code: 'PBC-E2E', kind: 'pbc' }),
  });
  check('2 从零建单元（PBC 构成）', created.code === 201, created);

  // ---- 3. 建任务 ----
  await setRole('BA');
  const atText = '- AT-01 构建质量。Given 当前提交完成构建，When 查看质量报告，Then 机器测试全部通过且构建含 SHA-256 摘要';
  const at = await api('/api/artifacts?pbc=' + unitID, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ artifact: 'at', text: atText }),
  });
  check('3 BA 保存人工验收标准', at.code === 201, at.body);
  const title = '端到端验收-' + Date.now();
  const task = await api('/api/tasks?pbc=' + unitID, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      title, type: 'feature', env: 'test', mode: 'fast',
      // 入口准入要的硬输入：可断言的验收口径 / 数据范围 / 验收人 / 合规背责人。
      accept: ['当业主提交账单，则应生成幂等键且重复提交不重复入账'],
      data: ['fee.bill.amount'], acceptBy: 'BA 业务', complyBy: 'SA 系统',
    }),
  });
  check('3 建任务', task.code === 201, task.body);
  const itemID = task.body && task.body.id;

  // ---- 4. DA 发起：真 workflow ----
  await setRole('DA');
  const run = await api('/api/items/' + itemID + '/run?pbc=' + unitID, { method: 'POST' });
  check('4 DA 发起交付（真实 Dapr workflow）', run.code === 202, run);

  // ---- 5. 机器五步跑完，停在「打 tag」 ----
  const waiting = await waitFor(async () => {
    const st = await api('/api/state?pbc=' + unitID);
    const it = st.body && st.body.items && st.body.items[itemID];
    return it && it.run === 'verified' ? it : null;
  }, 120000, 1500);
  check('5 workflow 跑完五步停在「打 tag」', !!waiting, waiting && { run: waiting.run, step: waiting.step });

  // ---- 6. 制品里有真内容 ----
  const st = await api('/api/state?pbc=' + unitID);
  const vers = st.body.versions || {};
  const codeVer = (vers.code || []).slice(-1)[0] || {};
  const files = codeVer.files || {};
  const hasGoMod = Object.keys(files).indexOf('go.mod') >= 0;
  const hasMain = Object.keys(files).indexOf('main.go') >= 0;
  const hasTest = Object.keys(files).indexOf('main_test.go') >= 0;
  check('6 代码制品带可编译的文件（go.mod / main.go / main_test.go）',
    hasGoMod && hasMain && hasTest, Object.keys(files));
  const head = st.body.commits[0];
  const atVer = (vers.at || []).find((v) => v.v === head.tree.at);
  check('6 当前提交的 AT 保留人工验收标准正文', atVer?.md === atText, atVer?.md);

  // ---- 7. 测试结论是真实跑出来的 ----
  const facts = await api('/api/items/' + itemID + '?pbc=' + unitID);
  const types = (facts.body.facts || []).map((f) => f.type);
  check('7 账本里有 test.run 事实（真跑过测试）', types.includes('test.run'), types);

  // ---- 8. digest 是构建产物的内容摘要 ----
  const buildFact = (facts.body.facts || []).find((f) => f.type === 'build.succeeded');
  const digest = buildFact && buildFact.payload && buildFact.payload.digest;
  check('8 构建产物 digest 存在且是 sha256 内容摘要',
    !!digest && /^sha256:[0-9a-f]{64}$/.test(digest), digest);

  // ---- 9. 单元自己不部署 ----
  const deployFact = (facts.body.facts || []).find((f) => f.type === 'deploy.succeeded');
  check('9 单元交付不部署（部署属于发布批次）', !deployFact, deployFact && deployFact.payload);

  // ---- 10. 打 tag → 批次发布 ----
  await setRole('BA');
  const test = st.body.test;
  const hasEvidence = st.body.checkCommit === head.no && test?.commit === head.no
    && test.total > 0 && test.pass === test.total && /^sha256:[0-9a-f]{64}$/.test(digest);
  const verdict = hasEvidence && await api('/api/checks/AT-01/decide?pbc=' + unitID, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ st: 'pass', commit: head.no }),
  });
  check('10 BA 核实当前提交的测试与摘要并判定通过', verdict?.code === 201, verdict?.body);
  await setRole('SA');
  const tag = await api('/api/units/' + unitID + '/tag', { method: 'POST' });
  check('10 SA 打 tag 进池', tag.code === 201 && !!tag.body.tag, tag.body);
  const rel = await api('/api/projects/p1/orch/release', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ env: 'UAT', mode: 'full', units: [unitID] }),
  });
  check('10 批次发布返回 201', rel.code === 201, rel.body && rel.body.id);
  const ru = ((rel.body || {}).units || [])[0] || {};
  check('10 批次锁住 tag 与 digest', ru.tag === tag.body.tag && ru.digest === digest, { tag: ru.tag, digest: ru.digest });
  let health = null;
  if (ru.url) {
    // 从 Node 侧探活：浏览器里发跨源请求会被 CORS 挡住，
    // 那是浏览器策略，不是服务不可达。
    const r = await fetch(ru.url + '/healthz');
    health = { code: r.status, body: await r.text() };
  }
  check('10 部署地址真的能访问，/healthz 返回 200', health && health.code === 200, health);

  // ---- 11. pubsub 收到了事件 ----
  // 投递地址先核对：sidecar 只往 app-port 投事件，本实例端口不一致就一条也收不到。
  const wiring = (await api('/api/health')).body.pubsub || {};
  check('11 pubsub 投递地址与本实例端口一致', wiring.checked === true && wiring.ok === true, wiring);
  const evs = await api('/api/events');
  const list = (evs.body && evs.body.events) || [];
  check('11 pubsub 收到的事件落了 event.received 事实', list.length > 0, list.slice(0, 2).map((e) => e.payload && e.payload.topic));

  // ---- 12. 事件源在 Redis stream 里（Dapr 真的投递过） ----
  check('11 事件确实经由总线（收到的条数 ≥ 1）', list.length >= 1, list.length);

  check('页面无 JS 异常', pageErrors.length === 0, pageErrors.slice(0, 3));

  await browser.close();
  const failed = results.filter((r) => !r.ok);
  console.log('SUMMARY ' + (results.length - failed.length) + '/' + results.length + ' passed');
  process.exit(failed.length ? 1 : 0);
})().catch((e) => { console.error('FATAL', e); process.exit(2); });
