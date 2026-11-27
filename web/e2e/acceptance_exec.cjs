// 验收「执行侧是真的」：跑完一条交付后，逐条核对账本里的证据。
//
// v3.2 起单元只做到「可以交付」：
//   - 制品里的代码能编译（有 go.mod / main.go / main_test.go）
//   - test.run 的条数是 go test 的真实输出（不是编的条数）
//   - build.succeeded 的 digest 是内容摘要（sha256: + 64 位十六进制）
//   - 单元自己不再部署：账本里没有 deploy.succeeded，工作项停在「待打 tag」
//   - 打 tag 后由项目级批次真部署：deploy.succeeded 的地址真的能访问，/healthz 200
//   - 种子写的历史 actor 是 seed，与真跑出来的事实区分得开
//   - pubsub 收到的事件落了 event.received 事实
//
// 用法：BASE=http://127.0.0.1:8080 node acceptance_exec.cjs
const { chromium } = require('playwright');
const { requirePubsub } = require('./require_pubsub.cjs');
const { mkdirSync } = require('node:fs');
const { join } = require('node:path');
const CHROME = '/Users/nukzhang/Library/Caches/ms-playwright/chromium-1217/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing';
const BASE = process.env.BASE || 'http://127.0.0.1:8080';
const RUN = process.env.RUN_TAG || String(Date.now()).slice(-6);

const results = [];
function check(name, ok, detail) {
  results.push({ name, ok: !!ok, detail });
  console.log((ok ? 'PASS ' : 'FAIL ') + name + (detail === undefined ? '' : ' :: ' + JSON.stringify(detail)));
}

(async () => {
  await requirePubsub(BASE);
  const browser = await chromium.launch({ executablePath: CHROME });
  const ctx = await browser.newContext();
  await ctx.addInitScript(() => localStorage.setItem('i3-theme', 'light'));
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

  // ---- 1. 建一个单元，在它上面跑完整条链 ----
  await role('PA');
  const unit = 'exec' + RUN;
  const cu = await api('/api/units', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id: unit, name: '执行侧验收单元', kind: 'pbc', proj: 'p1' }),
  });
  check('0 建执行侧验收单元', cu.code === 201, cu.body);

  await role('BA');
  const at = await api('/api/artifacts?pbc=' + unit, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ artifact: 'at', text: '- AT-01 构建质量。Given 当前提交完成构建，When 查看质量报告，Then 机器测试全部通过且构建含 SHA-256 摘要' }),
  });
  check('1 BA 保存人工验收标准', at.code === 201, at.body);
  const title = '执行侧验收-' + Date.now();
  const task = await api('/api/tasks?pbc=' + unit, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      title, type: 'feature', env: 'test', mode: 'fast',
      // 入口准入要的硬输入：可断言的验收口径 / 数据范围 / 验收人 / 合规背责人。
      accept: ['当提交执行侧单据，则应返回幂等键且重复提交不重复入账'],
      data: ['exec.record.id'], acceptBy: 'BA 业务', complyBy: 'SA 系统',
    }),
  });
  check('1 建工作项', task.code === 201, task.body);
  const id = task.body && task.body.id;

  await role('DA');
  const run = await api('/api/items/' + id + '/run?pbc=' + unit, { method: 'POST' });
  check('1 发起交付（真实 Dapr workflow）', run.code === 202, run.body);

  // 等机器五步跑完（真跑测试 + 真构建要时间）。
  let item = null;
  const deadline = Date.now() + 5 * 60 * 1000;
  while (Date.now() < deadline) {
    const st = await api('/api/state?pbc=' + unit);
    const it = st.body && st.body.items && st.body.items[id];
    if (it && ['verified', 'blocked', 'waiting', 'published'].includes(it.run)) { item = it; break; }
    await new Promise((r) => setTimeout(r, 2000));
  }
  // 没跑完就把卡在哪一步、哪句原因打出来：只报「null」等于把排查推给人。
  check('1 workflow 五步跑完，停在「打 tag」', !!item && item.run === 'verified',
    item ? { run: item.run, step: item.step, blocked: item.blocked } : { run: '(仍 running)', unit: unit });

  // ---- 2. 制品里有真内容（能编译） ----
  const st = (await api('/api/state?pbc=' + unit)).body;
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

  // ---- 5. 单元自己不部署 ----
  const deploys = st.deploys || [];
  check('5 单元交付不部署：账本里没有 deploy.succeeded', deploys.length === 0, deploys.length);

  // ---- 5.1 人能在页面上直接看到这些证据 ----
  await page.evaluate((u) => {
    const rows = Array.from(document.querySelectorAll('.sidebar .trow'));
    const p = rows.find((x) => x.textContent.includes('收费与工单数字化'));
    if (p) p.click();
  }, unit);
  await page.waitForTimeout(700);
  // 按单元 code 精确点：同一账本上跑过多轮时侧栏会有多个同名单元，
  // 按名字找会点进别人的账，digest 自然对不上（质量报告那段 #43 复跑时遇过）。
  // 点没点中要能当场看出来：选错账时 digest 对不上，但原因在「点在谁身上」，
  // 把点击前后侧栏的选中行报出来，而不是只报一句 digest 不一致（#44）。
  const picked = await page.evaluate(async (id) => {
    const sel = () => {
      const on = document.querySelector('.sidebar .trow.on[data-unit]');
      return on ? on.getAttribute('data-unit') : null;
    };
    const el = document.querySelector('.sidebar [data-unit="' + id + '"]');
    const before = sel();
    if (el) el.click();
    await new Promise((r) => setTimeout(r, 600));
    return { found: !!el, before, after: sel() };
  }, unit);
  check('5.1 侧栏点中本次单元（不是同名的上一轮）', picked.found && picked.after === unit, picked);
  await page.waitForTimeout(300);
  await page.evaluate(() => {
    const s = Array.from(document.querySelectorAll('.sidebar .trow.sub2')).find((x) => x.textContent.trim().startsWith('构建'));
    if (s) s.click();
  });
  await page.waitForTimeout(600);
  await page.evaluate(() => {
    const t = Array.from(document.querySelectorAll('.ptab')).find((x) => x.textContent.includes('质量报告'));
    if (t) t.click();
  });
  // 切单元会触发一次重拉（App.vue 里 watch S.pbcId），质量报告要用新账本的
  // test / builds 才出现。固定等 900ms 会读到上一个项目的旧报告，digest 对不上——
  // 所以等到本单元的摘要出现为止，超时再断言并带上此刻页面里真实的摘要（#44）。
  const shortDigest = String(digest).slice(0, 19);
  const pageText = await (async () => {
    const end = Date.now() + 10000;
    for (;;) {
      const t = await page.evaluate(() => (document.querySelector('#app') || {}).textContent || '');
      if (t.includes(shortDigest) || Date.now() > end) return t;
      await new Promise((r) => setTimeout(r, 300));
    }
  })();
  const shot = page
    ? await page.evaluate(() => (document.querySelector('#app').textContent.match(/sha256:[0-9a-f]+/) || [''])[0]).catch(() => '')
    : '';
  check('5 质量报告里看得到真证据（测试条数 / digest）',
    /\d+\/\d+ 通过/.test(pageText) && pageText.includes(shortDigest),
    { digest: shortDigest, shown: shot, hasPass: /\d+\/\d+ 通过/.test(pageText), unit: picked.after });

  const gateCard = page.locator('.card').filter({ has: page.getByRole('heading', { name: /^交付四闸/ }) });
  const gateSummary = await gateCard.locator('.ch .tag').evaluate((tag) => ({
    text: tag.textContent.trim(), warning: tag.classList.contains('t-warn'),
  }));
  const visualMark = await gateCard.locator('.achip').filter({ hasText: '原型还原' }).locator('b').innerText();
  const gates = st.gates.all;
  const unknown = gates.filter((gate) => gate.unknown);
  check('5 判不了的门禁用问号且不计入通过数（#78）',
    gates.length === 4 && unknown.length === 1 && unknown[0].key === 'visual' &&
    gateSummary.text === '3/4 通过 · 1 判不了' && gateSummary.warning && visualMark === '?',
    { summary: gateSummary, visualMark, unknown: unknown.map((gate) => gate.key) });

  await page.locator('.ptab').filter({ hasText: 'Backlog' }).click();
  await page.getByRole('row').filter({ has: page.getByRole('cell', { name: id, exact: true }) })
    .getByRole('button', { name: '查看' }).click();
  const badge = await page.locator('.stage .cr-state').evaluate((el) => ({
    text: el.textContent.trim(), cls: el.className,
  }));
  const chain = page.locator('.card').filter({ has: page.getByRole('heading', { name: '执行链路', exact: true }) });
  const label = await chain.locator('.ch .muted').innerText();
  const rendered = await chain.locator('.tl-item').count();
  const human = await chain.locator('.tl-item').filter({ hasText: '人工闸' }).allTextContents();
  check('5 工作项详情显示待打 tag，不把 verified 当草稿（#74）',
    item.run === 'verified' && badge.text === '待打 tag' && badge.cls.split(' ').includes('verified'), badge);
  check('5 执行链路标题按账本步骤计数，打 tag 是人工闸（#74）',
    rendered === item.steps.length && rendered === 6 && label === `${rendered} 步` &&
    human.length === 1 && human[0].includes('打 tag'), { label, rendered, human });
  const stage = page.locator('.stage');
  const verify = stage.getByRole('button', { name: '前往验证', exact: true });
  const workItemResults = stage.locator('.card').filter({ has: page.getByRole('heading', { name: '这一轮真跑出来的结论' }) });
  await workItemResults.waitFor();
  check('5 已构建工作项引导到验证，工作项结果未显示部署地址',
    await verify.isVisible() && await workItemResults.getByText('部署地址', { exact: true }).count() === 0
      && await stage.getByRole('button', { name: /退回修改|发布生产/ }).count() === 0,
    { unit, item: id, run: item.run, step: item.step });
  if (process.env.SHOTS) {
    mkdirSync(process.env.SHOTS, { recursive: true });
    await page.screenshot({ path: join(process.env.SHOTS, 'issue75-work-item.png') });
  }
  if (await verify.isVisible()) await verify.click();
  check('5 前往验证打开当前单元的验收清单和交付物',
    await stage.locator('.ac-chip').filter({ hasText: 'AT-01' }).isVisible()
      && await stage.getByRole('heading', { name: '交付物', exact: true }).isVisible()
      && await page.locator('.sidebar .trow.on[data-unit]').getAttribute('data-unit') === unit);
  await page.locator('.sidebar .trow.sub2').filter({ hasText: '方案' }).click();
  await stage.locator('.ptab').filter({ hasText: '提交历史' }).click();
  check('5 方案页的当前提交指向验证页打 tag',
    await stage.getByText('验收通过后，SA 在验证页打 tag。', { exact: true }).isVisible());
  const homeRow = page.locator('.sidebar .trow').filter({ hasText: '工作台' }).first();
  await homeRow.click();
  const tabs = await stage.locator('.home-tab').allTextContents();
  const badgeText = await homeRow.locator('.badge').allTextContents();
  check('5 工作台与侧栏使用同一待办计数',
    tabs.map((tab) => tab.trim()).join(' / ') === '我的待办 / 我发起的'
      && Number(badgeText[0] || '0') === await stage.locator('.task').count(), { tabs, badge: badgeText[0] || '0' });

  // ---- 6. 种子写的历史与真跑的事实分得开 ----
  // 种子事实查种子单元（fee）；真跑的事实查这次新建的单元。
  const facts = ((((await api('/api/facts?pbc=' + unit)).body || {}).facts) || []);
  const seedFacts = ((((await api('/api/facts?pbc=fee')).body || {}).facts) || []).filter((f) => f.actor === 'seed');
  const realFacts = facts.filter((f) => f.actor !== 'seed');
  check('6 种子事实 actor 是 seed（演示数据可识别）', seedFacts.length > 0, seedFacts.length);
  check('6 真跑出来的事实 actor 不是 seed', realFacts.length > 0, realFacts.map((f) => f.actor).slice(0, 4));

  // ---- 7. pubsub 收到的事件落了事实 ----
  // 先核对投递地址：sidecar 只往它配置的 app-port 投事件。本实例换个端口起，
  // 下面必然是 0 条——把根因直接报出来，别让人对着空数组猜。
  const wiring = (await api('/api/health')).body.pubsub || {};
  check('7 pubsub 投递地址与本实例端口一致', wiring.checked === true && wiring.ok === true, wiring);
  const evs = (await api('/api/events')).body.events || [];
  check('7 pubsub 收到的事件落了 event.received 事实', evs.length > 0, evs.slice(0, 2).map((e) => e.payload && e.payload.topic));

  // ---- 7.1 workflow 的每一步都真的过了总线 ----
  // 五步 + verified 收口 = 6 条 item.changed。
  const itemEvents = evs.filter((e) => e.payload && e.payload.topic === 'item.changed' &&
    e.payload.data && e.payload.data.item === id);
  const stepEvents = itemEvents.filter((e) => e.payload.data.kind === 'step' || e.payload.data.kind === 'verified');
  check('7 workflow 每一步都经总线发过事件（不是装饰品）',
    stepEvents.length >= 6, { 收到: itemEvents.length, 其中步骤: stepEvents.length });

  // ---- 8. 打 tag + 批次发布：部署挪到项目级 ----
  await role('BA');
  const hasEvidence = st.checkCommit === head.no && st.test?.commit === head.no
    && st.test.total > 0 && st.test.pass === st.test.total && /^sha256:[0-9a-f]{64}$/.test(digest);
  const verdict = hasEvidence && await api('/api/checks/AT-01/decide?pbc=' + unit, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ st: 'pass', commit: head.no }),
  });
  check('8 BA 核实当前提交的测试与摘要并判定通过', verdict?.code === 201, verdict?.body);
  await role('SA');
  // 发布失败时不崩：后面的 check 记 FAIL 并带原因。以前这里一失败，
  // 后面每个 check 都是 null.find 的 TypeError，整轮塌成 FATAL，
  // 看不出到底还坏了哪些（#52）。
  const tag = await api('/api/units/' + unit + '/tag', { method: 'POST' });
  const tagOk = tag.code === 201 && !!(tag.body && tag.body.tag);
  check('8 SA 打 tag 进池', tagOk, tag.body);

  const b = {};
  const ru = {};
  let relErr = null;
  if (!tagOk) {
    relErr = { reason: '打 tag 没成，批次发布无从发起', tag: tag.body };
  } else {
    const rel = await api('/api/projects/p1/orch/release', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ env: 'UAT', mode: 'full', units: [unit] }),
    });
    if (rel.code !== 201) relErr = { code: rel.code, body: rel.body };
    else Object.assign(b, rel.body || {});
  }
  check('8 批次发布返回 201', !relErr, relErr || b.id);
  Object.assign(ru, (b.units || [])[0] || {});
  check('8 批次锁住该单元的 tag 与 digest',
    !!ru.tag && ru.tag === (tag.body || {}).tag && ru.digest === digest,
    { tag: ru.tag, digest: ru.digest });
  const depState = (await api('/api/state?pbc=' + unit)).body || {};
  check('8 部署事实落在单元账本（deploy.succeeded 带批次号）',
    !!ru.url && (depState.deploys || []).some((d) => d.url === ru.url), ru.url);
  let health = null;
  if (ru.url) {
    health = await fetch(ru.url + '/healthz');
    health = { code: health.status, body: await health.text() };
  } else {
    health = relErr || { reason: '批次里没有该单元' };
  }
  check('8 部署地址能访问且 /healthz 返回 200', !!(health && health.code === 200), health);

  // ---- 9. 整批回滚，进程真的收掉 ----
  if (!b.id) {
    check('9 整批回滚返回 200', false, { reason: '没有批次可回滚', relErr });
    check('9 回滚后部署进程真的停了', false, { reason: '没有批次可回滚' });
  } else {
    const rb = await api('/api/projects/p1/orch/rollback', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id: b.id, note: '执行侧验收回滚' }),
    });
    check('9 整批回滚返回 200', rb.code === 200, rb.body);
    if (ru.url) {
      await new Promise((r) => setTimeout(r, 600));
      let after = null;
      try { after = (await fetch(ru.url + '/healthz')).status; } catch { after = 0; }
      check('9 回滚后部署进程真的停了', after !== 200, after);
    } else {
      check('9 回滚后部署进程真的停了', false, { reason: '没有部署地址（发布未成功）' });
    }
  }

  check('页面无 JS 异常', pageErrors.length === 0, pageErrors.slice(0, 3));

  await browser.close();
  const failed = results.filter((r) => !r.ok);
  console.log('SUMMARY ' + (results.length - failed.length) + '/' + results.length + ' passed');
  process.exit(failed.length ? 1 : 0);
})().catch((e) => { console.error('FATAL', e); process.exit(2); });
