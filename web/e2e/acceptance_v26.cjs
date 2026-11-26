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
    .filter((id) => ['fee', 'qa', 'dun', 'dunx', 'ticket'].indexOf(id) >= 0);
  check('1 种子单元四个、只有 PBC / Agent 两种构成（ticket 已撤）',
    seededUnits.length === 4 && seededUnits.indexOf('ticket') < 0 &&
    boot.units.some((u) => u.startsWith('qa:agent:qa-agent')) &&
    boot.units.some((u) => u.startsWith('dun:pbc:-')) &&
    boot.units.some((u) => u.startsWith('dunx:agent:dun-agent')),
    boot.units);
  check('1 Agent 工厂：5 名平台员工 + 2 个交付产物',
    boot.agents.length === 7 && boot.agents.includes('qa-agent') && boot.agents.includes('dun-agent'), boot.agents);

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
    const a = await fetch(base + '/api/agents/qa-agent').then((r) => r.json());
    return { digest: a.digest, built: a.builtDigest, ver: a.ver };
  }, BASE);
  // 每次用唯一标记，保证「内容真的变了」——否则重复跑同一条断言没有意义。
  const MARK = '验收改动-' + Date.now();
  await page.fill('.as-cfg textarea', '你是质检员。读会话原文与检索到的口径，逐条检查：\n1) 是否出现承诺疗效、绝对化用语\n2) 说的与口径字典是否一致\n3) 客户负面情绪是否被接住\n\n输出 JSON：{"risk":"high|normal","hits":[{"rule":"R-03","quote":"...","why":"..."}]}\n没有依据就写 unknown，不要猜。\n（' + MARK + '）');
  await page.click('.as-top button:has-text("保存编排")');
  await page.waitForTimeout(1200);
  const afterFlow = await page.evaluate(async (base) => {
    const a = await fetch(base + '/api/agents/qa-agent').then((r) => r.json());
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
    const a = await fetch(base + '/api/agents/qa-agent').then((r) => r.json());
    const g = a.gates.find((x) => x.key === 'agent-digest');
    return { ver: a.ver, digest: a.digest, built: a.builtDigest, gate: g.ok };
  }, BASE);
  check('3 构建后版本号前进且 digest 闸转绿',
    afterBuild.ver !== before.ver && afterBuild.gate === true && afterBuild.built === afterBuild.digest,
    { ver: afterBuild.ver, gate: afterBuild.gate });

  // ---- 4. 试跑这一条 → 沉淀为评测用例 ----
  const caseBefore = await page.evaluate(async (base) => {
    const a = await fetch(base + '/api/agents/qa-agent').then((r) => r.json());
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
    const a = await fetch(base + '/api/agents/qa-agent').then((r) => r.json());
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

  // ---- 8. PBC 单元：催缴策略（v3.0 起没有组合单元）----
  await page.click('.sidebar .trow.sub:has-text("催缴策略")');
  await page.waitForTimeout(800);
  await page.click('.trow.sub2:has-text("原型")');
  await page.waitForTimeout(800);
  const dunProto = await page.evaluate(() => ({
    text: (document.querySelector('.stage') || {}).textContent || '',
  }));
  check('8 催缴策略是 PBC 单元：原型页是线框与批注（来自账本）',
    /线框/.test(dunProto.text) && /批注/.test(dunProto.text), dunProto.text.slice(0, 80));

  await page.click('.trow.sub2:has-text("构建")');
  await page.waitForTimeout(600);
  await page.click('.ptab:has-text("质量报告")');
  await page.waitForTimeout(900);
  const dunQ = await page.evaluate(() => ({
    text: (document.querySelector('.stage') || {}).textContent || '',
  }));
  check('8 催缴策略质量报告是四闸（不再有组合的合并门禁）',
    /引用闭合/.test(dunQ.text) && !/合并门禁/.test(dunQ.text), dunQ.text.slice(0, 80));

  // ---- 9. Agent 工厂详情：编排摘要 + 去编排页 ----
  await page.click('.rolebtn.x');
  await page.waitForTimeout(700);
  const workers = await page.evaluate(() => ({
    rows: document.querySelectorAll('.agent-row').length,
  }));
  check('9 Agent 工厂上半列出 2 个交付产物', workers.rows === 2, workers.rows);
  await page.locator('.agent-row:has-text("质检 Agent")').click();
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

  // ---- 10. 工具白名单：编排越界 → 闸红 / 试跑留痕 / 对话拒绝 ----
  // 这条闸曾经只看「名单非空」就写「无越权」：四闸全绿也能真的读账单。
  // 这一段不假设账本是干净的：先按同一条规则（只认 cfg.tool / cfg.tools）
  // 自己算一遍越界项，再核对闸的说法和它一致；然后故意把编排弄脏跑一轮，
  // 最后原样写回去，不给下一轮留一个越界的编排。
  const outsideOf = (flow, tools) => {
    const out = [];
    for (const n of (flow && flow.nodes) || []) {
      const c = n.cfg || {};
      const names = [];
      if (typeof c.tool === 'string' && c.tool) names.push(c.tool);
      if (Array.isArray(c.tools)) c.tools.forEach((t) => typeof t === 'string' && t && names.push(t));
      for (const t of names) if (tools.indexOf(t) < 0) out.push({ tool: t, node: n.id, n: n.n });
    }
    return out;
  };
  await page.click('.rolebtn:has-text("DA")');
  await page.waitForTimeout(600);
  await page.click('.sidebar .trow.sub:has-text("交付质检 Agent")');
  await page.waitForTimeout(700);
  await page.click('.trow.sub2:has-text("构建")');
  await page.waitForTimeout(900);
  const tgBase = await page.evaluate(async (base) => {
    const a = await fetch(base + '/api/agents/qa-agent').then((r) => r.json());
    const g = (a.gates || []).find((x) => x.key === 'agent-tools') || {};
    const st = await fetch(base + '/api/state?pbc=qa').then((r) => r.json());
    return { flow: a.flow, tools: a.tools || [], gates: g, chats: (st.chats || []).length };
  }, BASE);
  const baseOutside = outsideOf(tgBase.flow, tgBase.tools);
  check('10 工具闸的说法与编排一致（没有越界就不许红，有就越界点名）',
    baseOutside.length === 0
      ? tgBase.gates.ok === true && !/名单外/.test(tgBase.gates.detail || '')
      : tgBase.gates.ok === false &&
        baseOutside.every((o) => (tgBase.gates.detail || '').includes(o.tool)),
    { outside: baseOutside, gate: tgBase.gates });

  // 弄脏：把「读会话」换成名单里没有的工具。bill.read / shell.exec 执行器都认得，
  // 这样被拦不是因为「这个工具不存在」，而是因为「白名单里没有它」。
  const dirty = JSON.parse(JSON.stringify(tgBase.flow));
  const dirtyNode = dirty.nodes.find((n) => n.id === 'n2');
  const foreign = tgBase.tools.indexOf('bill.read') < 0 ? 'bill.read' : 'shell.exec';
  dirtyNode.cfg.tool = foreign;
  const putDirty = await page.evaluate(async ([base, flow]) => {
    const r = await fetch(base + '/api/agents/qa-agent/flow', {
      method: 'PUT', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ flow }),
    });
    return { code: r.status, body: await r.json().catch(() => null) };
  }, [BASE, dirty]);
  check('10 越界编排可以保存（越界不是保存错误，是闸的事）', putDirty.code === 200, putDirty.body);

  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1500);
  // 刷新之后侧栏回到初始状态：必须重新选中「交付质检 Agent」这一行，
  // 否则点到的是别的单元的「构建」页（PBC 单元没有编排工作台）。
  await page.click('.sidebar .trow.sub:has-text("交付质检 Agent")');
  await page.waitForTimeout(700);
  await page.click('.trow.sub2:has-text("构建")');
  await page.waitForTimeout(900);
  const dirtyView = await page.evaluate(async (base) => {
    const a = await fetch(base + '/api/agents/qa-agent').then((r) => r.json());
    const g = (a.gates || []).find((x) => x.key === 'agent-tools') || {};
    return { gate: g, text: (document.querySelector('.stage') || {}).textContent || '' };
  }, BASE);
  check('10 名单外的工具让 agent-tools 闸变红（不是「判不了」），并点名工具与节点',
    dirtyView.gate.ok === false && dirtyView.gate.unknown !== true &&
    (dirtyView.gate.detail || '').includes(foreign) && (dirtyView.gate.detail || '').includes('n2'),
    dirtyView.gate);
  check('10 越界时 Detail 不再写「无越权」', !/无越权/.test(dirtyView.gate.detail || ''), dirtyView.gate.detail);
  check('10 编排页工具权限卡点名越界（写 Agent 的人看得见是哪一步）',
    /编排用了名单外的工具/.test(dirtyView.text) && dirtyView.text.includes(foreign), null);

  // 试跑：照样跑完，但被拒的节点要留痕，不能只显示「跑通」。
  await page.click('.as-top button:has-text("试跑这一条")');
  await page.waitForTimeout(1500);
  const tgRun = await page.evaluate(async (base) => {
    const pills = Array.from(document.querySelectorAll('.as-dbg-r .tag')).map((x) => x.textContent.trim());
    const stuck = Array.from(document.querySelectorAll('.as-chain span.is-blocked')).map((x) => x.textContent.trim());
    const a = await fetch(base + '/api/agents/qa-agent').then((r) => r.json());
    const nodes = ((a.trialRun || {}).nodes || []).map((n) => ({ n: n.n, blocked: (n.blocked || []).join(' ') }));
    return { pills, stuck, nodes };
  }, BASE);
  check('10 试跑状态是「跑完 · 被拒 N 步」，不再只写「跑通」',
    tgRun.pills.some((p) => /跑完 · 被拒 \d+ 步/.test(p)), tgRun.pills);
  check('10 被拒的节点在链上单独标出（读会话）',
    tgRun.stuck.length >= 1 && /读会话/.test(tgRun.stuck.join('')), tgRun.stuck);
  check('10 试跑结果进账本：tool.blocked 可从 /api/agents/{id} 查回',
    tgRun.nodes.some((n) => n.n === '读会话' && n.blocked.includes(foreign)), tgRun.nodes);

  // 对话验收不能拿一轮被拦下的执行拼回复：拒绝，且一条对话事实都不落。
  // 对话是 BA 的活（验收台），DA 点这个口子会被 403 拦住——先切回 BA。
  await page.click('.rolebtn:has-text("BA")');
  await page.waitForTimeout(600);
  const chat = await page.evaluate(async (base) => {
    const r = await fetch(base + '/api/units/qa/chat', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ text: '你们这个热玛吉能维持多久' }),
    });
    const body = await r.json().catch(() => null);
    const st = await fetch(base + '/api/state?pbc=qa').then((x) => x.json());
    return { code: r.status, body, chats: (st.chats || []).length };
  }, BASE);
  check('10 被拒的一轮不出客户回复（400 且说明是白名单拦的）',
    chat.code === 400 && /工具白名单/.test(JSON.stringify(chat.body)), chat.body);
  const expectedChat400 = chat.code === 400;
  check('10 被拒不落对话事实（账本上没有「客户问过、AI 答过」）',
    chat.chats === tgBase.chats, { before: tgBase.chats, after: chat.chats });

  // 复原：把编排写回原样，闸回到原来的说法，红条消失。写编排又是 DA 的活。
  await page.click('.rolebtn:has-text("DA")');
  await page.waitForTimeout(600);
  const putBack = await page.evaluate(async ([base, flow]) => {
    const r = await fetch(base + '/api/agents/qa-agent/flow', {
      method: 'PUT', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ flow }),
    });
    return r.status;
  }, [BASE, tgBase.flow]);
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1500);
  await page.click('.rolebtn:has-text("DA")');
  await page.waitForTimeout(600);
  await page.click('.sidebar .trow.sub:has-text("交付质检 Agent")');
  await page.waitForTimeout(700);
  await page.click('.trow.sub2:has-text("构建")');
  await page.waitForTimeout(900);
  const back = await page.evaluate(async (base) => {
    const a = await fetch(base + '/api/agents/qa-agent').then((r) => r.json());
    const g = (a.gates || []).find((x) => x.key === 'agent-tools') || {};
    return { gate: g, text: (document.querySelector('.stage') || {}).textContent || '' };
  }, BASE);
  check('10 编排写回原样后闸回到原状态（这一段不留脏数据）',
    putBack === 200 && back.gate.ok === tgBase.gates.ok &&
    back.gate.detail === tgBase.gates.detail &&
    /编排用了名单外的工具/.test(back.text) === (baseOutside.length > 0),
    { put: putBack, gate: back.gate, was: tgBase.gates });

  // ---- 11. 验证页不再展示机器自检 ----
  await page.click('.trow.sub2:has-text("验证")');
  await page.waitForTimeout(800);
  const verifyPage = await page.evaluate(() => ({
    text: (document.querySelector('.stage') || {}).textContent || '',
  }));
  check('11 验证页是验收台（不再堆机器自检）',
    /已验/.test(verifyPage.text) && !/合并门禁/.test(verifyPage.text), verifyPage.text.slice(0, 60));

  // 第 10 段故意打了一条会被白名单拒掉的对话：那一条 400 是脚本自造的，
  // 浏览器会同时记一条 console 错误。除此之外不该有任何异常。
  const ownErrors = (e) => /status of 400/.test(e) && expectedChat400;
  const unexpected = httpErrors.filter(
    (e) => !e.endsWith('/favicon.ico') && !(expectedChat400 && e === '400 ' + BASE + '/api/units/qa/chat'),
  );
  const unexpectedPageErrors = pageErrors.filter((e) => !ownErrors(e));
  check('页面无 JS 异常（除脚本自造的对话 400 外）', unexpectedPageErrors.length === 0, unexpectedPageErrors.slice(0, 3));
  check('无 HTTP 错误（除脚本自造的对话 400 外）', unexpected.length === 0, unexpected.slice(0, 5));

  await browser.close();
  const failed = results.filter((r) => !r.ok);
  console.log('SUMMARY ' + (results.length - failed.length) + '/' + results.length + ' passed');
  process.exit(failed.length ? 1 : 0);
})().catch((e) => { console.error('FATAL', e); process.exit(2); });