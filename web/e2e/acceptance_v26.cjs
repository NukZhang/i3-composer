// v2.6 验收：Vue 前端 + 真实 Dapr 后端（端口由 BASE 决定）。
// 断言「所见即账本」：编排改 digest、验收判定落事实、bug 进 backlog。
const { chromium } = require('playwright');
const CHROME = '/Users/nukzhang/Library/Caches/ms-playwright/chromium-1217/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing';
const BASE = process.env.BASE || 'http://127.0.0.1:8091';

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
  const httpErrors = [];
  page.on('pageerror', (e) => pageErrors.push(String(e)));
  page.on('console', (m) => { if (m.type() === 'error') pageErrors.push('console: ' + m.text()); });
  page.on('response', (r) => { if (r.status() >= 400) httpErrors.push(r.status() + ' ' + r.url()); });

  await page.goto(BASE + '/', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1800);

  // ---- 1. 首屏：三个单元、七个 worker ----
  const boot = await page.evaluate(async (base) => {
    const [units, agents] = await Promise.all([
      fetch(base + '/api/units').then((r) => r.json()),
      fetch(base + '/api/agents').then((r) => r.json()),
    ]);
    return {
      units: units.units.map((u) => u.id + ':' + u.kind + ':' + (u.agentId || '-')),
      agents: agents.agents.map((a) => a.id),
      root: (document.querySelector('#app') || {}).textContent || '',
    };
  }, BASE);
  check('1 首屏挂载 Vue', boot.root.includes('工作台'), boot.root.slice(0, 40));
  // 只数种子带来的那几个单元：别的验收脚本（acceptance_full）会在同一本账上
  // 建新单元，那是正常的，不该让这条断言误报。
  const seededUnits = boot.units
    .map((u) => u.split(':')[0])
    .filter((id) => ['fee', 'qa', 'dun', 'ticket'].indexOf(id) >= 0);
  check('1 种子单元只剩三个构成（fee/qa/dun，ticket 已撤）',
    seededUnits.length === 3 && seededUnits.indexOf('ticket') < 0 &&
    boot.units.some((u) => u.startsWith('qa:agent:x-qc')) && boot.units.some((u) => u.startsWith('dun:hybrid:x-dun')),
    boot.units);
  check('1 Agent 工厂是七名 worker（含 X-质检员）',
    boot.agents.length === 7 && boot.agents.includes('x-qc'), boot.agents);

  // ---- 2. Agent 单元的构建页 = 编排工作台 ----
  // 编排与构建是 DA 的活：先切角色（会话由服务端签发，前端改不动权限）。
  await page.click('.rolebtn:has-text("DA")');
  await page.waitForTimeout(800);
  await page.click('.sidebar .trow.sub:has-text("交付质检 Agent")');
  await page.waitForTimeout(700);
  await page.click('.trow.sub2:has-text("构建")');
  await page.waitForTimeout(900);
  const studio = await page.evaluate(() => ({
    tabs: Array.from(document.querySelectorAll('.ptab')).map((x) => x.textContent.trim()),
    listItems: document.querySelectorAll('.as-li').length,
    nodes: document.querySelectorAll('.anode').length,
    edges: document.querySelectorAll('.as-canvas path').length,
    cfg: (document.querySelector('.as-cfg') || {}).textContent || '',
    tools: Array.from(document.querySelectorAll('.card table.tb tr')).map((r) => r.textContent.trim()),
    text: (document.querySelector('.stage') || {}).textContent || '',
  }));
  check('2 构建页有 Agent 编排 / 质量报告两个新 tab',
    studio.tabs.some((t) => t.includes('Agent 编排')) && studio.tabs.some((t) => t.includes('质量报告')), studio.tabs);
  check('2 编排工作台有 7 个节点 + 连线', studio.listItems === 7 && studio.nodes === 7 && studio.edges >= 7,
    { li: studio.listItems, node: studio.nodes, edge: studio.edges });
  check('2 节点清单可点选（节点配置面板有内容）', studio.cfg.includes('节点 ID'), studio.cfg.slice(0, 60));
  check('2 工具权限表多一列「用在哪」',
    studio.tools.some((t) => t.includes('session.read')) && studio.tools.some((t) => t.includes('接到')), studio.tools.slice(0, 4));
  check('2 知识库依赖卡在编排页', /知识库依赖/.test(studio.text) && /产品口径字典/.test(studio.text), studio.text.match(/知识库依赖/));
  check('2 导出 Dapr Agent 定义（折叠）', /导出 Dapr Agent 定义/.test(studio.text), null);

  // 点一个模型节点 → 配置面板显示提示词
  await page.locator('.as-li:has-text("合规判定")').click();
  await page.waitForTimeout(400);
  const nodeCfg = await page.evaluate(() => ({
    cfg: (document.querySelector('.as-cfg') || {}).textContent || '',
    hasPrompt: !!document.querySelector('.as-cfg textarea'),
  }));
  check('2 模型节点配置面板显示提示词（可编辑）', nodeCfg.hasPrompt && /提示词/.test(nodeCfg.cfg), nodeCfg.cfg.slice(0, 80));

  // ---- 3. 改提示词 → 保存编排 → digest 变 → 构建 → 版本号变 ----
  const before = await page.evaluate(async (base) => {
    const a = await fetch(base + '/api/agents/x-qc').then((r) => r.json());
    return { digest: a.digest, built: a.builtDigest, ver: a.ver };
  }, BASE);
  // 每次用唯一标记，保证「内容真的变了」——否则重复跑同一条断言没有意义。
  const MARK = '验收改动-' + Date.now();
  await page.fill('.as-cfg textarea', '你是质检员。读会话原文与检索到的口径，逐条检查：\n1) 是否出现承诺疗效、绝对化用语\n2) 说的与口径字典是否一致\n3) 客户负面情绪是否被接住\n\n输出 JSON：{"risk":"high|normal","hits":[{"rule":"R-03","quote":"...","why":"..."}]}\n没有依据就写 unknown，不要猜。\n（' + MARK + '）');
  await page.click('.as-top button:has-text("保存编排")');
  await page.waitForTimeout(1200);
  const afterFlow = await page.evaluate(async (base) => {
    const a = await fetch(base + '/api/agents/x-qc').then((r) => r.json());
    const g = a.gates.find((x) => x.key === 'agent-digest');
    const node = (a.flow.nodes || []).find((n) => n.t === 'llm');
    return { digest: a.digest, built: a.builtDigest, gate: g.ok, detail: g.detail, prompt: (node.cfg || {}).prompt };
  }, BASE);
  check('3 改编排后 digest 变了', afterFlow.digest !== before.digest && afterFlow.prompt.includes(MARK),
    { before: before.digest, after: afterFlow.digest });
  check('3 改了没构建：digest 闸变红', afterFlow.gate === false, afterFlow.detail);

  await page.click('.as-top button:has-text("构建")');
  await page.waitForTimeout(1200);
  const afterBuild = await page.evaluate(async (base) => {
    const a = await fetch(base + '/api/agents/x-qc').then((r) => r.json());
    const g = a.gates.find((x) => x.key === 'agent-digest');
    return { ver: a.ver, digest: a.digest, built: a.builtDigest, gate: g.ok };
  }, BASE);
  check('3 构建后版本号前进且 digest 闸转绿',
    afterBuild.ver !== before.ver && afterBuild.gate === true && afterBuild.built === afterBuild.digest,
    { ver: afterBuild.ver, gate: afterBuild.gate });

  // ---- 4. 试跑这一条 → 沉淀为评测用例 ----
  const caseBefore = await page.evaluate(async (base) => {
    const a = await fetch(base + '/api/agents/x-qc').then((r) => r.json());
    return (a.cases || []).length;
  }, BASE);
  await page.click('.as-top button:has-text("试跑这一条")');
  await page.waitForTimeout(1200);
  const ran = await page.evaluate(() => ({
    out: (document.querySelector('.as-dbg-r') || {}).textContent || '',
    chain: document.querySelectorAll('.as-chain span').length,
  }));
  check('4 试跑有输出与逐节点耗时链', /risk/.test(ran.out) && ran.chain >= 4, { chain: ran.chain, out: ran.out.slice(0, 60) });
  await page.click('.as-dbg-r button:has-text("沉淀为评测用例")');
  await page.waitForTimeout(1200);
  const caseAfter = await page.evaluate(async (base) => {
    const a = await fetch(base + '/api/agents/x-qc').then((r) => r.json());
    return { n: (a.cases || []).length, last: (a.cases || []).slice(-1)[0] };
  }, BASE);
  check('4 沉淀的用例进评测集', caseAfter.n === caseBefore + 1 && caseAfter.last && caseAfter.last.v === '试跑沉淀',
    { before: caseBefore, after: caseAfter.n });

  // ---- 5. 质量报告 tab（机器自检） ----
  await page.click('.ptab:has-text("质量报告")');
  await page.waitForTimeout(900);
  const quality = await page.evaluate(() => ({
    text: (document.querySelector('.stage') || {}).textContent || '',
    metrics: document.querySelectorAll('.ev-m').length,
  }));
  check('5 质量报告有四闸 + 评测台 + 知识库准确度',
    /交付四闸/.test(quality.text) && /实验对比/.test(quality.text) && /知识库准确度/.test(quality.text),
    { m: quality.metrics });
  check('5 知识库准确度有召回/忠实度/引用四格', quality.metrics >= 9, quality.metrics);
  // 视觉比对还没接：PBC 单元的四闸里，这一条必须标成「判不了」，
  // 不能画成绿勾冒充「比过了、没差异」。Agent 单元没有这条闸，
  // 所以要看 PBC（fee）的投影。
  const feeGates = await page.evaluate(async (b) => {
    const r = await fetch(b + '/api/state?pbc=fee');
    const j = await r.json();
    return (j.gates && j.gates.all) || [];
  }, BASE);
  const visual = feeGates.find((g) => g.key === 'visual') || {};
  check('5 视觉比对未接入时，这条闸标成判不了（不是假装通过）',
    visual.unknown === true && /未接入/.test(visual.detail || ''),
    visual);

  // ---- 6. 验证页 = 真人验收台（BA 的活：验收判定是 acceptAT） ----
  await page.click('.rolebtn:has-text("BA")');
  await page.waitForTimeout(800);
  await page.click('.trow.sub2:has-text("验证")');
  await page.waitForTimeout(900);
  const bench = await page.evaluate(() => ({
    chips: Array.from(document.querySelectorAll('.ac-chip')).map((x) => x.textContent.trim()),
    text: (document.querySelector('.stage') || {}).textContent || '',
    term: !!document.querySelector('.ac-term'),
  }));
  check('6 验证页是验收台（AC 清单 + 对话终端）', bench.chips.length === 5 && bench.term, bench.chips);
  check('6 判定面板有 Given/When/Then', /Given/.test(bench.text) && /When/.test(bench.text) && /Then/.test(bench.text), null);

  // 判定一条通过 → 落事实
  await page.click('.ac-chip:has-text("AR-2")');
  await page.waitForTimeout(400);
  await page.click('button:has-text("通过")');
  await page.waitForTimeout(1200);
  const decided = await page.evaluate(async (base) => {
    const st = await fetch(base + '/api/state?pbc=qa').then((r) => r.json());
    return (st.checks || []).map((c) => c.id + ':' + c.st);
  }, BASE);
  check('6 判定「通过」进账本', decided.includes('AR-2:pass'), decided);

  // 不通过 → 提 bug → bug 进 backlog
  await page.click('.ac-chip:has-text("AR-3")');
  await page.waitForTimeout(400);
  await page.click('button:has-text("不提过")');
  await page.waitForTimeout(500);
  const bugForm = await page.evaluate(() => ({
    open: !!document.querySelector('.bug-steps'),
    title: (document.querySelector('.bug-title') || {}).value || '',
    steps: (document.querySelector('.bug-steps') || {}).value || '',
  }));
  check('6 「不提过」展开 bug 表单（标题预填、步骤自动带入）',
    bugForm.open && bugForm.title.length > 3 && bugForm.steps.length > 0, bugForm);
  await page.fill('.bug-title', '情绪没接住：客户不满时客服直接报价');
  await page.click('button:has-text("提交")');
  await page.waitForTimeout(1500);
  const afterBug = await page.evaluate(async (base) => {
    const st = await fetch(base + '/api/state?pbc=qa').then((r) => r.json());
    const items = Object.values(st.items || {});
    return {
      checks: (st.checks || []).map((c) => c.id + ':' + c.st),
      defects: (st.defects || []).map((d) => d.id + '/' + d.ac + '/' + d.sev),
      backlog: items.filter((i) => i.type === 'defect').map((i) => i.id + ':' + i.t),
    };
  }, BASE);
  check('6 bug 落账且 AC 标不通过', afterBug.checks.includes('AR-3:fail') && afterBug.defects.length >= 1, afterBug);
  check('6 bug 进 backlog（成了一条修复项）', afterBug.backlog.length >= 1, afterBug.backlog);

  // ---- 7. Agent 原型页 = 质检复核一屏 ----
  await page.click('.trow.sub2:has-text("原型")');
  await page.waitForTimeout(800);
  const proto = await page.evaluate(() => ({
    text: (document.querySelector('.stage') || {}).textContent || '',
    rows: document.querySelectorAll('.card .row[style*="cursor: pointer"]').length,
  }));
  check('7 Agent 原型页是「质检复核」一屏（可点会话 + 判定依据 + 人工动作）',
    /质检复核/.test(proto.text) && /Agent 判定依据/.test(proto.text) && /转人工复核/.test(proto.text),
    proto.text.slice(0, 80));

  // ---- 8. 组合单元：催缴业务台 + 合并门禁 ----
  await page.click('.sidebar .trow.sub:has-text("智能催缴")');
  await page.waitForTimeout(800);
  await page.click('.trow.sub2:has-text("原型")');
  await page.waitForTimeout(800);
  const dunProto = await page.evaluate(() => ({
    text: (document.querySelector('.stage') || {}).textContent || '',
  }));
  check('8 组合项原型页是「催缴业务台」（策略/任务/话术三块）',
    /催缴业务台/.test(dunProto.text) && /策略配置/.test(dunProto.text) && /话术审核/.test(dunProto.text),
    dunProto.text.slice(0, 80));

  await page.click('.trow.sub2:has-text("构建")');
  await page.waitForTimeout(600);
  await page.click('.ptab:has-text("质量报告")');
  await page.waitForTimeout(900);
  const hybrid = await page.evaluate(() => ({
    text: (document.querySelector('.stage') || {}).textContent || '',
  }));
  check('8 组合项质量报告有合并门禁且不能发布',
    /合并门禁/.test(hybrid.text) && /不能发布/.test(hybrid.text) && /20\/22/.test(hybrid.text),
    hybrid.text.match(/不能发布|可以发布/));

  // ---- 9. Agent 工厂详情：编排摘要 + 去编排页 ----
  await page.click('.rolebtn.x');
  await page.waitForTimeout(700);
  const workers = await page.evaluate(() => ({
    rows: document.querySelectorAll('.agent-row').length,
  }));
  check('9 Agent 工厂列出 7 名 worker', workers.rows === 7, workers.rows);
  await page.locator('.agent-row:has-text("X-质检员")').click();
  await page.waitForTimeout(700);
  const detail = await page.evaluate(() => ({
    tabs: Array.from(document.querySelectorAll('.ptab')).map((x) => x.textContent.trim()),
    text: (document.querySelector('.stage') || {}).textContent || '',
  }));
  check('9 质检员详情有五个阶段 tab', detail.tabs.length === 5, detail.tabs);
  await page.click('.ptab:has-text("构建")');
  await page.waitForTimeout(700);
  const agBuild = await page.evaluate(() => ({
    text: (document.querySelector('.stage') || {}).textContent || '',
    chips: Array.from(document.querySelectorAll('.achip')).map((x) => x.textContent.trim()),
  }));
  check('9 Agent 详情构建页是编排摘要 + 节点一览',
    /编排摘要/.test(agBuild.text) && /节点一览/.test(agBuild.text) && agBuild.chips.length === 7, agBuild.chips);
  check('9 有「去编排页」按钮', /去编排页/.test(agBuild.text), null);

  // ---- 10. 验证页不再展示机器自检 ----
  await page.click('.trow.sub2:has-text("验证")');
  await page.waitForTimeout(800);
  const verifyPage = await page.evaluate(() => ({
    text: (document.querySelector('.stage') || {}).textContent || '',
  }));
  check('10 验证页是验收台（不再堆机器自检）',
    /已验/.test(verifyPage.text) && !/合并门禁/.test(verifyPage.text), verifyPage.text.slice(0, 60));

  check('页面无 JS 异常', pageErrors.length === 0, pageErrors.slice(0, 3));
  const unexpected = httpErrors.filter((e) => !e.endsWith('/favicon.ico'));
  check('无 HTTP 错误', unexpected.length === 0, unexpected.slice(0, 5));

  await browser.close();
  const failed = results.filter((r) => !r.ok);
  console.log('SUMMARY ' + (results.length - failed.length) + '/' + results.length + ' passed');
  process.exit(failed.length ? 1 : 0);
})().catch((e) => { console.error('FATAL', e); process.exit(2); });