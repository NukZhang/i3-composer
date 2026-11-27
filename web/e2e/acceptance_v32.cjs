// v3.2 全链路验收：四阶段 → 打 tag 进池 → 项目级发布编排（批次）→ 整批回滚。
//
// 判据仍然是账本事实，不是页面文案：
//   - 交付：item.verified（机器五步跑完，没有 deploy.succeeded）
//   - 打 tag：tag.created（HEAD 上，payload 带 digest）
//   - 发布：release.batch.created（锁 tag/digest；PBC 真起进程，Agent 无 URL）
//   - 回滚：release.batch.rolledback，且部署进程真的停了
//
// 用法：BASE=http://127.0.0.1:8080 RUN_TAG=v32 node acceptance_v32.cjs
const { chromium } = require('playwright');
const { requirePubsub } = require('./require_pubsub.cjs');
const fs = require('fs');
const path = require('path');

const CHROME = '/Users/nukzhang/Library/Caches/ms-playwright/chromium-1217/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing';
const BASE = process.env.BASE || 'http://127.0.0.1:8080';
const SHOTS = process.env.SHOTS || path.join(__dirname, '../../docs/selftest-v32/shots');
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

async function requireSeedLedger() {
  const endpoint = BASE + '/api/projects/p1/orch';
  const response = await fetch(endpoint, { signal: AbortSignal.timeout(5000) });
  if (!response.ok) {
    throw new Error(`无法检查验收账本：GET ${endpoint} 返回 HTTP ${response.status}`);
  }
  const { units, batches } = await response.json();
  const expected = new Set(['fee', 'qa', 'dun', 'dunx']);
  const projectUnits = (units || []).map((unit) => unit.id);
  const unexpected = projectUnits.filter((id) => !expected.has(id));
  const missing = [...expected].filter((id) => !projectUnits.includes(id));
  const existingBatches = (batches || []).map((batch) => batch.id);
  if (unexpected.length || missing.length || existingBatches.length) {
    throw new Error(`v32 需要干净的演示账本（项目 p1）；额外单元：${unexpected.join(', ') || '无'}；` +
      `缺少种子单元：${missing.join(', ') || '无'}；已有发布批次：${existingBatches.join(', ') || '无'}。` +
      '请切换到全新账本并启用 SEED_DEMO=1 后再跑。');
  }
}

(async () => {
  await requirePubsub(BASE);
  await requireSeedLedger();
  fs.mkdirSync(SHOTS, { recursive: true });
  const browser = await chromium.launch({ executablePath: CHROME });
  const ctx = await browser.newContext({ viewport: { width: 1500, height: 950 } });
  // 截图统一用明主题：正文是白底黑字，贴进文档和文章里不会变成一块黑屏。
  // 在应用启动前写入偏好，App.vue 挂载时按它加 body.light。
  await ctx.addInitScript(() => {
    try {
      localStorage.setItem('i3-theme', 'light');
    } catch {
      /* 忽略 */
    }
  });
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
    const file = String(++shotNo).padStart(2, '0') + '-' + name + '.png';
    await page.screenshot({ path: path.join(SHOTS, file), fullPage: false });
    return file;
  };

  await page.goto(BASE + '/', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1500);

  // ---- 1. 侧栏：四阶段，发布编排入口在项目行 ----
  await page.evaluate(() => {
    const rows = Array.from(document.querySelectorAll('.sidebar .trow'));
    const p = rows.find((x) => x.textContent.includes('收费与工单数字化'));
    if (p) p.click();
  });
  await page.waitForTimeout(900);
  const stages = await page.evaluate(() => Array.from(document.querySelectorAll('.sidebar .trow.sub2')).map((x) => x.textContent.trim()));
  check('1 单元只有四个阶段（方案/原型/构建/验证）', stages.length === 4 && !stages.includes('发布'), stages);
  const ico = await page.evaluate(() => !!document.querySelector('.sidebar .orch-ico'));
  check('1 项目行有「发布编排」图标', ico);
  await shot('侧栏四阶段');

  // ---- 2. 发布编排页：编排链 / 就绪检查 / 勾选 ----
  await page.locator('.sidebar .orch-ico').first().click();
  await page.waitForTimeout(1200);
  const orch = (await api('/api/projects/p1/orch')).body;
  const ready = orch.units.filter((u) => u.ready).length;
  const blocked = orch.units.filter((u) => !u.ready).length;
  check('2 编排链列出项目下全部单元', orch.units.length === 4, orch.units.map((u) => u.id + ':' + u.kind));
  check('2 就绪 / 阻塞与账本一致（种子 2 就绪 2 阻塞）', ready === 2 && blocked === 2, { ready, blocked });
  check('2 页面渲染 4 张单元卡', await page.locator('.orch-card').count() === 4, await page.locator('.orch-card').count());
  await shot('发布编排');

  // 勾选：只选 Agent → 2 个；全不选 → 按钮禁用
  await page.evaluate(() => { const b = Array.from(document.querySelectorAll('.stage button')).find((x) => x.textContent.startsWith('只 Agent')); if (b) b.click(); });
  await page.waitForTimeout(500);
  const selCount = await page.evaluate(() => document.querySelectorAll('.orch-card:not(.off)').length);
  check('2 筛选「只 Agent」后选中 2 个单元', selCount === 2, selCount);
  await page.evaluate(() => { const b = Array.from(document.querySelectorAll('.stage button')).find((x) => x.textContent.startsWith('全部')); if (b) b.click(); });
  await page.waitForTimeout(400);

  // ---- 3. 批次发布：qa + dun 一起上，锁两个 tag；Agent 无 URL ----
  await role('SA');
  await page.evaluate(() => {
    const cards = Array.from(document.querySelectorAll('.orch-card'));
    for (const c of cards) {
      const name = c.textContent;
      if (!name.includes('交付质检 Agent') && !name.includes('催缴策略')) c.click();
    }
  });
  await page.waitForTimeout(400);
  const rel = await api('/api/projects/p1/orch/release', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ env: 'SIT', mode: 'full', units: ['qa', 'dun'] }),
  });
  check('3 SA 发布混合批次返回 201', rel.code === 201, rel.body && rel.body.id);
  const batch = rel.body || {};
  check('3 批次锁住两个 tag', (batch.units || []).length === 2 && batch.units.every((u) => u.tag), batch.units && batch.units.map((u) => u.name + ' ' + u.tag));
  const pbcUnit = (batch.units || []).find((u) => u.binary);
  const agUnit = (batch.units || []).find((u) => !u.binary);
  check('3 PBC 真部署（有 URL），Agent 产物没有进程（无 URL）',
    !!(pbcUnit && pbcUnit.url) && !(agUnit && agUnit.url), { pbc: pbcUnit && pbcUnit.url, agent: agUnit && agUnit.url });
  let health = null;
  if (pbcUnit && pbcUnit.url) {
    const r = await fetch(pbcUnit.url + '/healthz');
    health = r.status;
  }
  check('3 部署地址 /healthz 真的 200', health === 200, health);
  const verifyPath = `/api/projects/p1/releases/${batch.id}/units/${pbcUnit?.id}/health`;
  const proof = await api(verifyPath);
  check('3 同源核对入口返回批次锁定的单元与版本',
    pbcUnit?.healthUrl === verifyPath && !agUnit?.healthUrl && proof.code === 200 &&
      proof.body.ok === true && proof.body.unit === pbcUnit.id && proof.body.commit === pbcUnit.commit &&
      proof.body.tag === pbcUnit.tag && proof.body.digest === pbcUnit.digest && proof.body.batch === batch.id,
    { path: pbcUnit?.healthUrl, code: proof.code, proof: proof.body });
  // 批次是账本外的东西：靠 /api/health 之外的一条「有变化」通知驱动重拉，
  // 给它几秒，而不是写死 400ms。
  const seen = await waitFor(
    () => page.evaluate((id) => document.querySelector('.stage').textContent.includes(id), batch.id),
    8000, 400,
  );
  check('3 页面历史批次里出现该批次', !!seen, batch.id);
  const link = page.locator(`.stage a[href="${verifyPath}"]`);
  check('3 发布页提供宿主机可访问的同源核对入口', await link.count() === 1, await link.count());
  await shot('批次发布后');

  // ---- 4. 整批回滚：进程真的停了 ----
  const rb = await api('/api/projects/p1/orch/rollback', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id: batch.id, note: '验收回滚' }),
  });
  check('4 整批回滚返回 200', rb.code === 200 && rb.body.status === 'rolledback', rb.body);
  const revoked = await fetch(new URL(verifyPath, BASE));
  check('4 回滚后同源核对入口失效', revoked.status === 404, await revoked.json());
  if (pbcUnit && pbcUnit.url) {
    await new Promise((r) => setTimeout(r, 600));
    let after = null;
    try { after = (await fetch(pbcUnit.url + '/healthz')).status; } catch { after = 0; }
    check('4 回滚后部署进程真的停了', after !== 200, after);
  }
  const rb2 = await api('/api/projects/p1/orch/rollback', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id: batch.id }),
  });
  check('4 重复回滚被拒（批次已经回滚过）', rb2.code === 400, rb2.body);

  // ---- 5. 未打 tag 的单元进不了批次 ----
  const relFee = await api('/api/projects/p1/orch/release', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ env: 'SIT', mode: 'full', units: ['fee'] }),
  });
  check('5 未打 tag 的单元被拦，且点名', relFee.code === 400 && /还没打 tag/.test(relFee.body.error) && /收费缴费/.test(relFee.body.error), relFee.body);

  // ---- 6. 打 tag 的前置：验收没全过就打不动 ----
  const tagFee = await api('/api/units/fee/tag', { method: 'POST' });
  check('6 验收未全过的单元打不了 tag（点名还差几条）', tagFee.code === 400 && /还差/.test(tagFee.body.error), tagFee.body);
  const tagDunx = await api('/api/units/dunx/tag', { method: 'POST' });
  check('6 话术 Agent 打不了 tag（评测/验收未过）', tagDunx.code === 400 && /还差/.test(tagDunx.body.error), tagDunx.body);

  // ---- 7. 新单元全链路：建单元 → 交付（五步）→ verified → 打 tag → 进池 ----
  await role('PA');
  const unitID = 'v32x' + RUN;
  const cu = await api('/api/units', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id: unitID, name: '验收单元' + RUN, kind: 'pbc', proj: 'p1' }),
  });
  check('7 PA 建单元（只剩 pbc / agent 两种）', cu.code === 201, cu.body);
  const cuHy = await api('/api/units', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id: 'hy' + RUN, name: '组合单元', kind: 'hybrid', proj: 'p1' }),
  });
  check('7 hybrid 被拒（一个单元只有一个 tag）', cuHy.code === 400 && /不再支持 hybrid/.test(cuHy.body.error), cuHy.body);

  await role('BA');
  const at = await api(`/api/artifacts?pbc=${unitID}`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ artifact: 'at', text: '- AT-01 构建质量。Given 当前提交完成构建，When 查看质量报告，Then 机器测试全部通过且构建含 SHA-256 摘要' }),
  });
  check('7 BA 保存人工验收标准', at.code === 201, at.body);
  const t = await api('/api/tasks?pbc=' + unitID, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      title: '验收单元 · 首次交付', type: 'new', env: 'test', mode: 'fast',
      // 入口准入要的硬输入：可断言的验收口径 / 数据范围 / 验收人 / 合规背责人。
      accept: ['当业主提交缴费单，则应生成幂等键且重复提交不重复入账'],
      data: ['fee.bill.amount'], acceptBy: 'BA 业务', complyBy: 'SA 系统',
    }),
  });
  const itemID = t.body && t.body.id;
  check('7 BA 建工作项', t.code === 201 && !!itemID, itemID);

  await role('DA');
  const run = await api(`/api/items/${itemID}/run?pbc=${unitID}`, { method: 'POST' });
  check('7 DA 发起交付', run.code === 202, run.body && run.body.instance);
  const verified = await waitFor(async () => {
    const st = (await api(`/api/state?pbc=${unitID}`)).body;
    const it = st && st.items && st.items[itemID];
    return it && it.run === 'verified' ? it : null;
  }, 180000, 1500);
  check('7 五步跑完，停在「打 tag」', !!verified && verified.step === 5, verified && { run: verified.run, step: verified.step });
  const facts = (await api(`/api/items/${itemID}?pbc=${unitID}`)).body.facts || [];
  const ft = facts.map((f) => f.type);
  check('7 账本有 test.run / build.succeeded，且没有 deploy.succeeded', ft.includes('test.run') && ft.includes('build.succeeded') && !ft.includes('deploy.succeeded'), ft);
  const tf = facts.find((f) => f.type === 'test.run');
  const bf = facts.find((f) => f.type === 'build.succeeded');
  check('7 测试是真跑出来的', !!tf && tf.payload.total > 0, tf && tf.payload);
  check('7 digest 是 sha256 内容摘要', !!bf && /^sha256:[0-9a-f]{64}$/.test(bf.payload.digest), bf && bf.payload.digest);

  // 验证页截图：交付物卡（打 tag 前）
  await page.evaluate((id) => {
    const rows = Array.from(document.querySelectorAll('.sidebar .trow'));
    const p = rows.find((x) => x.textContent.includes('收费与工单数字化'));
    if (p) p.click();
  }, null);
  await page.waitForTimeout(700);
  await page.evaluate((name) => {
    const rows = Array.from(document.querySelectorAll('.sidebar .trow'));
    const p = rows.find((x) => x.textContent.trim().startsWith(name));
    if (p) p.click();
  }, '验收单元' + RUN);
  await page.waitForTimeout(600);
  await page.evaluate(() => { const s = Array.from(document.querySelectorAll('.sidebar .trow.sub2')).find((x) => x.textContent.trim().startsWith('验证')); if (s) s.click(); });
  await page.waitForTimeout(900);
  await role('SA');
  check('7 人工验收待判定时页面禁用打 tag', await page.getByRole('button', { name: '打 tag 固化交付物' }).isDisabled());
  await shot('打tag前');

  const pending = await api(`/api/units/${unitID}/tag`, { method: 'POST' });
  check('7 人工验收待判定时 SA 不能打 tag', pending.code === 400 && /还差 1 条/.test(pending.body.error), pending.body);
  const st = (await api(`/api/state?pbc=${unitID}`)).body;
  const head = st.commits[0];
  const hasEvidence = st.checkCommit === head.no && st.test?.commit === head.no
    && st.test.total > 0 && st.test.pass === st.test.total && /^sha256:[0-9a-f]{64}$/.test(head.digest);
  await role('BA');
  const verdict = hasEvidence && await api(`/api/checks/AT-01/decide?pbc=${unitID}`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ st: 'pass', commit: head.no }),
  });
  check('7 BA 核实当前提交的测试与摘要并判定通过', verdict?.code === 201, verdict?.body);
  await role('SA');
  const tag = await api(`/api/units/${unitID}/tag`, { method: 'POST' });
  check('7 SA 打 tag 进池', tag.code === 201 && !!tag.body.tag, tag.body);
  const tag2 = await api(`/api/units/${unitID}/tag`, { method: 'POST' });
  check('7 重复打 tag 被拒', tag2.code === 400, tag2.body);
  const unitNow = ((await api('/api/units')).body.units || []).find((u) => u.id === unitID);
  check('7 单元 HEAD 上有 tag', !!unitNow && unitNow.headTag === tag.body.tag, unitNow && { head: unitNow.head, headTag: unitNow.headTag });
  const orch2 = (await api('/api/projects/p1/orch')).body;
  const ou = (orch2.units || []).find((u) => u.id === unitID);
  check('7 打 tag 后编排里就绪', !!ou && ou.ready === true, ou && { ready: ou.ready, reason: ou.reason });
  await page.waitForTimeout(400);
  await shot('打tag后');

  // ---- 8. Backlog（v3.2）：版本下拉 / 工作项在前 / story-task 分层 / 文档收起 ----
  await page.evaluate(() => {
    const rows = Array.from(document.querySelectorAll('.sidebar .trow'));
    const p = rows.find((x) => x.textContent.trim().startsWith('收费缴费'));
    if (p) p.click();
  });
  await page.waitForTimeout(700);
  await page.evaluate(() => { const s = Array.from(document.querySelectorAll('.sidebar .trow.sub2')).find((x) => x.textContent.trim().startsWith('构建')); if (s) s.click(); });
  await page.waitForTimeout(600);
  await page.evaluate(() => { const t = Array.from(document.querySelectorAll('.ptab')).find((x) => x.textContent.includes('Backlog')); if (t) t.click(); });
  await page.waitForTimeout(800);
  const bk = await page.evaluate(() => ({
    sel: !!document.querySelector('.ws select'),
    stories: document.querySelectorAll('.backlog-tb .bk-story').length,
    tasks: document.querySelectorAll('.backlog-tb .bk-task').length,
    docCollapsed: document.body.textContent.includes('文档默认收起'),
    order: (() => {
      const tb = document.querySelector('.backlog-tb');
      const doc = document.querySelector('.stage').textContent;
      return tb && doc.indexOf('工作项') < doc.indexOf('Backlog 文档');
    })(),
  }));
  check('8 Backlog 有版本下拉', bk.sel);
  check('8 story / task 分层渲染', bk.stories >= 3 && bk.tasks >= 3, { stories: bk.stories, tasks: bk.tasks });
  check('8 工作项在工作项表里、文档默认收起且在后面', bk.docCollapsed && bk.order, bk);
  await shot('Backlog');

  // ---- 9. Agent 工厂：平台员工与交付产物分开 ----
  await page.evaluate(() => { const b = Array.from(document.querySelectorAll('.rolebtn')).find((x) => x.textContent.includes('Agent 工厂')); if (b) b.click(); });
  await page.waitForTimeout(1000);
  const fac = await page.evaluate(() => document.querySelector('.stage').textContent);
  const agents = (await api('/api/agents')).body.agents;
  const workers = agents.filter((a) => !a.product).length;
  const products = agents.filter((a) => a.product).length;
  check('9 平台员工 5 名 / 交付产物 2 个', workers === 5 && products === 2, { workers, products });
  check('9 页面上没有「质检工程师 / 催缴专员」这类错位命名',
    !/质检工程师|催缴专员|UI设计师|业务分析员/.test(fac), fac.slice(0, 80));
  await shot('Agent工厂');

  // ---- 10. 项目页只剩两种构成 ----
  await page.evaluate(() => {
    const rows = Array.from(document.querySelectorAll('.sidebar .trow'));
    const p = rows.find((x) => x.textContent.includes('收费与工单数字化'));
    if (p) p.click();
  });
  await page.waitForTimeout(900);
  const proj = await page.evaluate(() => document.querySelector('.stage').textContent);
  check('10 项目页只有 PBC / Agent 两组，没有「PBC + Agent」', !proj.includes('PBC + Agent'), proj.slice(0, 120));

  // ---- 11. 回放：按 tag 打下去的那一刻重算投影 ----
  // 「凭什么能发」要能当场核对：闸是当时算的那组，不是今天的账本重算一遍。
  const rp = await api(`/api/units/${unitID}/replay?tag=${encodeURIComponent(tag.body.tag)}`);
  check('11 已打 tag 的单元可回放（200）', rp.code === 200, rp.body && rp.body.tag);
  check('11 回放停在打 tag 的那次提交', rp.code === 200 && rp.body.commit === tag.body.commit, rp.body && { commit: rp.body.commit, tagCommit: tag.body.commit });
  check('11 回放带出当时那组闸（四条，含「判不了」）',
    rp.code === 200 && (rp.body.gates || []).length === 4 && rp.body.gates.some((g) => g.unknown),
    rp.body && rp.body.gates);
  check('11 回放保留打 tag 时通过的人工验收',
    (rp.body.checks || []).some((c) => c.id === 'AT-01' && c.st === 'pass'), rp.body.checks);
  check('11 回放窗口右端是 tag.created 的 seq', rp.code === 200 && (rp.body.facts || []).some((f) => f.seq === rp.body.seq && f.type === 'tag.created'), rp.body && { seq: rp.body.seq });
  // 证据先于凭据落账：闸那条紧挨着 tag 那条。
  const tagFacts = (await api(`/api/facts?pbc=${unitID}`)).body.facts.map((f) => f.type);
  check('11 账本里 gate.evaluated 紧挨着 tag.created', tagFacts.indexOf('tag.created') >= 0 && tagFacts[tagFacts.indexOf('tag.created') + 1] === 'gate.evaluated', tagFacts.slice(0, 4));
  const rpBad = await api(`/api/units/${unitID}/replay?tag=v9.9.9`);
  check('11 不存在的 tag 回放被 400 拒绝并列出可选', rpBad.code === 400 && /v9\.9\.9/.test(rpBad.body.error), rpBad.body);
  const rpNone = await api('/api/units/nothing-here/replay');
  check('11 不存在的单元回放 404', rpNone.code === 404, rpNone.body);
  // 种子基线（dun v2.0.0）老账本没落闸：如实说没有，不拿今天补。
  const rpSeed = await api('/api/units/dun/replay?tag=v2.0.0');
  check('11 老基线回放 200 且如实说明「当时没记录闸门」',
    rpSeed.code === 200 && !rpSeed.body.gates && /没有记录/.test(rpSeed.body.gatesNote || ''),
    rpSeed.body && { gates: rpSeed.body.gates, note: rpSeed.body.gatesNote });

  // 界面：验证页交付物卡下面出现回放证据区。
  await page.evaluate((id) => {
    const rows = Array.from(document.querySelectorAll('.sidebar .trow'));
    const p = rows.find((x) => x.textContent.includes('收费与工单数字化'));
    if (p) p.click();
  }, null);
  await page.waitForTimeout(700);
  // 按 data-unit 精确点：同一账本上跑过多轮时同名单元不止一行，按名字会点进别人的账（#61）。
  await page.evaluate((id) => {
    const el = document.querySelector('.sidebar [data-unit="' + id + '"]');
    if (el) el.click();
  }, unitID);
  await page.waitForTimeout(600);
  await page.evaluate(() => { const s = Array.from(document.querySelectorAll('.sidebar .trow.sub2')).find((x) => x.textContent.trim().startsWith('验证')); if (s) s.click(); });
  // 等回放证据区真出现（切单元会重拉投影），别把渲染慢读成「没有回放」。
  await page.waitForSelector('.replay-box', { timeout: 5000 }).catch(() => {});
  const rpUI = await page.evaluate(() => {
    const box = document.querySelector('.replay-box');
    return box ? { text: box.textContent.trim().slice(0, 200), chips: box.querySelectorAll('.replay-gates .tag').length } : null;
  });
  check('11 验证页显示回放证据区（tag 与四闸）', !!rpUI && rpUI.text.includes(tag.body.tag) && rpUI.chips === 4, rpUI);
  if (rpUI) {
    await page.evaluate(() => { const b = Array.from(document.querySelectorAll('.replay-box button')).find((x) => x.textContent.includes('看依据')); if (b) b.click(); });
    await page.waitForTimeout(400);
  }
  await shot('回放');

  // ---- 12. 作废与复登：PA 管登记，单元自己的事实链始终保留（#40） ----
  const retiredID = 'retired' + RUN;
  const retiredRow = page.locator(`.sidebar [data-unit="${retiredID}"]`);
  const register = (name) => api('/api/units', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id: retiredID, name, kind: 'pbc', proj: 'p1' }),
  });
  const registryFacts = async () => (await api('/api/facts?pbc=units')).body.facts;
  await role('PA');
  const original = await register('作废复登验收单元');
  await retiredRow.waitFor({ timeout: 6000 }).catch(() => {});
  check('12 PA 登记后侧栏显示该单元', original.code === 201 && await retiredRow.count() === 1, original.code);
  const beforeRemoval = await registryFacts();
  const removalPath = '/api/units/' + retiredID + '?createdSeq=' + original.body?.registrySeq;
  await role('DA');
  const denied = await page.request.delete(BASE + removalPath);
  check('12 DA 作废返回 403 且注册表无新事实',
    denied.status() === 403 && (await registryFacts())[0].seq === beforeRemoval[0].seq, denied.status());
  await role('PA');
  const removed = await api(removalPath, { method: 'DELETE' });
  await retiredRow.waitFor({ state: 'detached', timeout: 6000 }).catch(() => {});
  const missing = await page.request.get(BASE + '/api/state?pbc=' + retiredID);
  const afterRemoval = (await registryFacts())[0];
  check('12 作废后侧栏消失、详情 404、只追加 PA 的 unit.removed',
    removed.code === 200 && await retiredRow.count() === 0 && missing.status() === 404 &&
    afterRemoval.seq === beforeRemoval[0].seq + 1 && afterRemoval.type === 'unit.removed' && afterRemoval.actor === 'PA',
    { removed: removed.code, missing: missing.status(), fact: afterRemoval });
  await shot('单元作废后');
  const registered = await register('同 ID 复登验收单元');
  await retiredRow.filter({ hasText: '同 ID 复登验收单元' }).waitFor({ timeout: 6000 }).catch(() => {});
  const reopened = await api('/api/state?pbc=' + retiredID);
  const audit = (await api('/api/facts?pbc=' + retiredID)).body.facts;
  check('12 同 ID 复登后侧栏和详情更新，原登记事实仍在',
    registered.code === 201 && await retiredRow.filter({ hasText: '同 ID 复登验收单元' }).count() === 1 &&
    reopened.code === 200 && reopened.body.name === '同 ID 复登验收单元' &&
    audit.length === 2 && audit.every((f) => f.type === 'unit.created'),
    { registered: registered.code, read: reopened.code, id: retiredID, audit });
  const staleRemoval = await page.request.delete(BASE + removalPath);
  check('12 旧登记的作废请求返回 409，复登不受影响',
    staleRemoval.status() === 409 && (await registryFacts())[0].seq === registered.body?.registrySeq, staleRemoval.status());
  await shot('单元复登后');

  // ---- 13. 健康度 ----
  const realErrors = pageErrors.filter((e) => !/Failed to load resource/.test(e));
  check('页面无 JS 异常', realErrors.length === 0, realErrors.slice(0, 3));
  const unexpected = httpErrors.filter((e) => !e.endsWith('/favicon.ico') && !/events\//.test(e)
    && !/\/api\/units/.test(e) && !/orch\/release/.test(e) && !/orch\/rollback/.test(e));
  check('无意外 HTTP 错误（脚本自造的 400 除外）', unexpected.length === 0, unexpected.slice(0, 5));

  fs.writeFileSync(path.join(SHOTS, 'summary.json'), JSON.stringify({
    run: RUN, orch: { ready, blocked }, batch, unit: unitID, tag: tag.body && tag.body.tag,
  }, null, 2));
  await browser.close();
  const failed = results.filter((r) => !r.ok);
  console.log('SUMMARY ' + (results.length - failed.length) + '/' + results.length + ' passed');
  console.log('SHOTS ' + SHOTS);
  process.exit(failed.length ? 1 : 0);
})().catch((e) => { console.error('FATAL', e); process.exit(2); });
