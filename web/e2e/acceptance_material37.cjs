// 材料入库 PBC 验收。用法：BASE=http://127.0.0.1:8080 node web/e2e/acceptance_material37.cjs
const assert = require('node:assert/strict');
const { randomBytes } = require('node:crypto');
const { mkdirSync, writeFileSync } = require('node:fs');
const { join } = require('node:path');
const { setTimeout: delay } = require('node:timers/promises');
const { chromium } = require('playwright');

const base = process.env.BASE || 'http://127.0.0.1:8080';
const shots = process.env.SHOTS;
const run = process.env.RUN_TAG || Date.now().toString(36);
const unit = 'mat37-' + run.toLowerCase().replace(/[^a-z0-9]/g, '').slice(0, 12) + '-' + randomBytes(2).toString('hex');
const project = 'p1';
const query = '?pbc=' + unit;
const testPath = '/pbc/' + unit + '/test/';
const sitPath = '/pbc/' + unit + '/sit/';
const material = 'MAT-STEEL';
const warehouse = 'WH-01';
const finance = '一次性进成本';
const purchase = { type: 'RKT-01', name: '采购入库', sourceNo: 'CG-TEST-001', sourceLabel: '采购合同', assetOwner: '公司资产' };
const supplied = { type: 'RKT-02', name: '甲供入库', sourceNo: 'DY-TEST-001', sourceLabel: '到货点验', assetOwner: '甲方资产' };

const criteria = {
  'AT-01': '类型默认值。Given 验收环境已有脱敏来源单，When 依次选择采购入库与甲供入库，Then 显示对应来源类型、资产归属和一次性进成本。',
  'AT-02': '确认入库。Given 钢材计划量为 120.000 吨，When 依次确认 80.400 吨和 39.600 吨，Then 分别生成入库单且余量准确扣至 0.000 吨。',
  'AT-03': '超计划拒绝。Given 钢材计划余量为 120.000 吨，When 确认 120.001 吨，Then 提示超计划且余量及台账均不变。',
  'AT-04': '台账追溯。Given 两条入库单已确认，When 逐条按入库单号查询，Then 能核对来源、材料、库房、实收数量和归属口径。',
  'AT-05': '运行健康。Given 当前提交的制品已在验收环境启动，When 点击检查服务健康，Then 返回 HTTP 200 并显示当前业务服务的提交与环境。',
};
const checkIds = Object.keys(criteria);
const atText = '# 材料入库业务验收\n\n' + Object.entries(criteria).map(([id, text]) => `- ${id} ${text}`).join('\n');

async function json(response, status) {
  const body = await response.text();
  assert.ok([status].flat().includes(response.status()), `${response.url()} 返回 ${response.status()}，预期 ${status}：${body.slice(0, 600)}`);
  return JSON.parse(body);
}

async function visible(page, selector, text) {
  await page.locator(selector).filter({ hasText: text }).waitFor({ state: 'visible' });
}

async function clickRequest(page, button, method, path, status) {
  const url = new URL(path, base).href;
  const [response] = await Promise.all([
    page.waitForResponse((res) => res.url() === url && res.request().method() === method),
    page.getByRole('button', { name: button, exact: true }).click(),
  ]);
  return json(response, status);
}

async function screenshot(page, name) {
  if (shots) await page.screenshot({ path: join(shots, 'material37-' + name + '.png'), fullPage: true });
}

async function chooseType(page, expected) {
  await page.getByLabel('入库类型', { exact: true }).selectOption(expected.type);
  assert.ok((await page.locator('#inbound-type option:checked').textContent()).startsWith(expected.name));
  await page.getByLabel('来源单号', { exact: true }).selectOption(expected.sourceNo);
  await visible(page, '#source-type', expected.sourceLabel);
  await visible(page, '#asset-owner', expected.assetOwner);
  await visible(page, '#finance-tag', finance);
}

async function checkHealth(page, path, commit, environment) {
  const health = await clickRequest(page, '检查服务健康', 'GET', path + 'healthz', 200);
  assert.equal(health.ok, true);
  assert.equal(health.unit, unit);
  assert.equal(health.commit, commit);
  assert.equal(health.environment.toLowerCase(), environment);
  await visible(page, '#health-status', /HTTP\s+200/);
  await visible(page, '#health-status', unit);
  await visible(page, '#health-status', commit);
  await visible(page, '#health-status', new RegExp('环境\\s+' + environment, 'i'));
  return health;
}

(async () => {
  if (shots) mkdirSync(shots, { recursive: true });
  const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH });
  try {
    const page = await browser.newPage({ baseURL: base, viewport: { width: 1440, height: 1000 } });
    const errors = [];
    page.on('pageerror', (error) => errors.push(error.message));
    const get = async (path) => json(await page.request.get(path), 200);
    const post = async (path, status, data) => json(await page.request.post(path, { data }), status);
    const role = async (name) => assert.equal((await post('/api/role', 200, { role: name })).role, name);

    console.log('材料入库验收单元', unit);
    await role('PA');
    await post('/api/units', 201, { id: unit, name: '材料入库验收 ' + unit, kind: 'pbc', proj: project, profile: 'material-inbound' });
    await role('BA');
    const at = await post('/api/artifacts' + query, 201, { artifact: 'at', text: atText });
    const { id: item } = await post('/api/tasks' + query, 201, {
      title: '核对材料入库的真实交付', type: 'feature', env: 'test', mode: 'fast',
      accept: Object.values(criteria), data: ['material_receipt.source_no', 'material_receipt.quantity'],
      acceptBy: 'BA 业务', complyBy: 'SA 系统',
    });
    await role('DA');
    const itemPath = '/api/items/' + item;
    const statePath = '/api/state' + query;
    const started = await post(itemPath + '/run' + query, 202);
    assert.ok(started.instance?.endsWith(item), '工作流实例应绑定工作项');

    let state;
    const deadline = Date.now() + 5 * 60 * 1000;
    while (Date.now() < deadline) {
      state = await get(statePath);
      if (['verified', 'blocked', 'waiting', 'published'].includes(state.items[item]?.run)) break;
      await delay(1000);
    }
    assert.equal(state.items[item]?.run, 'verified', JSON.stringify(state.items[item]));
    const head = state.commits[0];
    const { no: commit, digest } = head;
    assert.match(digest, /^sha256:[0-9a-f]{64}$/);
    assert.equal(state.checkCommit, commit);
    assert.deepEqual(state.checks.map(({ id, st }) => [id, st]), checkIds.map((id) => [id, 'todo']));
    const atVersion = state.versions.at.find((version) => version.v === head.tree.at);
    assert.equal(atVersion.source, '手动编辑');
    assert.equal(atVersion.md, atText);
    assert.equal(state.versions.at.find((version) => version.v === at.version)?.md, atText);

    const code = state.versions.code.find((version) => version.v === head.tree.code);
    const testFiles = Object.entries(code.files).filter(([name]) => name.endsWith('_test.go')).map(([, contents]) => contents).join('\n');
    const { facts: machineFacts } = await get(itemPath + query);
    const test = machineFacts.find((fact) => fact.type === 'test.run' && fact.payload.commit === commit);
    const build = machineFacts.find((fact) => fact.type === 'build.succeeded' && fact.payload.commit === commit);
    for (const id of checkIds) assert.match(testFiles, new RegExp('func Test' + id.replace('-', '') + '\\w*\\('));
    assert.equal(test?.actor, 'x-qa');
    assert.ok(test.payload.total >= checkIds.length, '机器测试至少覆盖五项入库行为');
    assert.equal(test.payload.pass, test.payload.total);
    assert.equal(state.test.commit, commit);
    assert.equal(state.test.total, test.payload.total);
    assert.equal(state.test.pass, test.payload.pass);
    assert.equal(build?.payload.digest, digest);
    assert.equal(build?.payload.binary, true);
    assert.equal((state.deploys || []).length, 0);
    console.log('PASS 当前提交的人工 AT、入库机器测试与二进制摘要', commit, test.payload.pass + '/' + test.payload.total, digest);

    await role('BA');
    const preview = await post('/api/units/' + unit + '/material-preview', [200, 201]);
    assert.equal(new URL(preview.url, base).href, new URL(testPath, base).href);
    assert.equal(preview.commit, commit);
    assert.equal(preview.digest, digest);
    const loaded = await page.goto(preview.url, { waitUntil: 'domcontentloaded' });
    assert.equal(loaded.status(), 200);
    await page.getByRole('heading', { name: '新建入库', exact: true }).waitFor();
    const options = async () => get(testPath + 'api/options');
    const receipts = async () => (await get(testPath + 'api/inbounds')).receipts;
    const firstOptions = await options();
    assert.equal(firstOptions.commit, commit);
    assert.equal(firstOptions.unit, unit);
    assert.equal(firstOptions.environment.toLowerCase(), 'test');
    const checkBalance = async (qty, count) => {
      const data = await options();
      assert.equal(data.materials.find(({ code }) => code === material).remainingQty, qty);
      assert.equal(data.receiptCount, count);
      await visible(page, '#plan-remaining', qty + ' 吨');
      await visible(page, '#receipt-count', new RegExp('已确认入库单数\\s+' + count + '$'));
    };

    await chooseType(page, purchase);
    await chooseType(page, supplied);
    await screenshot(page, '01-defaults');
    console.log('PASS AT-01 两种入库类型联动来源、资产归属及财务口径');

    await chooseType(page, purchase);
    await page.getByLabel('材料', { exact: true }).selectOption(material);
    await page.getByLabel('库房', { exact: true }).selectOption(warehouse);
    await checkBalance('120.000', 0);
    const quantity = page.getByLabel('入库数量', { exact: true });
    // 超计划先于任何成功入库，才能核对 0.001 吨的边界及零记账。
    await quantity.fill('120.001');
    const rejected = await clickRequest(page, '确认入库', 'POST', testPath + 'api/inbounds', 409);
    assert.match(rejected.error, /超计划/);
    await visible(page, '#form-status', '超计划');
    await checkBalance('120.000', 0);
    assert.deepEqual(await receipts(), []);
    await screenshot(page, '03-over-plan');
    console.log('PASS AT-03 超计划被拒且余量和台账保持不变');

    const confirmed = [];
    for (const [kind, amount, remaining] of [[purchase, '80.400', '39.600'], [supplied, '39.600', '0.000']]) {
      await chooseType(page, kind);
      await quantity.fill(amount);
      const receipt = await clickRequest(page, '确认入库', 'POST', testPath + 'api/inbounds', 201);
      assert.ok(receipt.receiptNo, '确认入库应返回可查询的单号');
      assert.notEqual(receipt.receiptNo, confirmed[0]?.receiptNo);
      assert.equal(receipt.quantity, amount);
      assert.equal(receipt.remainingQty, remaining);
      confirmed.push(receipt);
      await checkBalance(remaining, confirmed.length);
    }
    assert.equal((await receipts()).length, 2);
    await page.locator('#receipts tr').nth(1).waitFor({ state: 'visible' });
    await screenshot(page, '02-confirmed');
    console.log('PASS AT-02 两次确认各入账一次且计划余量准确扣至零', confirmed.map(({ receiptNo }) => receiptNo));

    for (const [kind, receipt] of [[purchase, confirmed[0]], [supplied, confirmed[1]]]) {
      await page.getByLabel('入库单号', { exact: true }).fill(receipt.receiptNo);
      const record = await clickRequest(page, '查询台账', 'GET', testPath + 'api/inbounds/' + encodeURIComponent(receipt.receiptNo), 200);
      assert.deepEqual(record, receipt);
      assert.equal(record.type, kind.type);
      assert.equal(record.sourceNo, kind.sourceNo);
      assert.equal(record.sourceLabel, kind.sourceLabel);
      assert.equal(record.materialCode, material);
      assert.equal(record.warehouseId, warehouse);
      assert.equal(record.unit, '吨');
      assert.equal(record.assetOwner, kind.assetOwner);
      assert.equal(record.financeTag, finance);
      await visible(page, '#ledger-status', '已找到入库单 ' + receipt.receiptNo);
      const row = page.locator('#receipts tr').filter({ hasText: receipt.receiptNo });
      await row.waitFor({ state: 'visible' });
      const text = await row.innerText();
      for (const value of [kind.name, kind.sourceNo, kind.sourceLabel, record.materialName, record.warehouseName, receipt.quantity, '吨', kind.assetOwner, finance]) {
        assert.ok(value && text.includes(value), '台账行缺少 ' + value);
      }
      assert.equal(await page.locator('#receipts tr').count(), 1);
    }
    await screenshot(page, '04-ledger');
    console.log('PASS AT-04 通过浏览器按入库单号查询并核对两笔台账');

    const testHealth = await checkHealth(page, testPath, commit, 'test');
    await screenshot(page, '05-test-health');
    console.log('PASS AT-05 验收页的真实服务健康检查', testHealth);

    state = await get(statePath);
    assert.equal(state.commits[0].no, commit);
    assert.equal(state.commits[0].digest, digest);
    assert.deepEqual(state.checks.map(({ st }) => st), checkIds.map(() => 'todo'));
    for (const id of checkIds) await post('/api/checks/' + id + '/decide' + query, 201, { st: 'pass', commit });
    await role('SA');
    const tagged = await post('/api/units/' + unit + '/tag', 201);
    assert.ok(tagged.tag);
    assert.equal(tagged.commit, commit);
    assert.equal(tagged.digest, digest);
    const replay = await get('/api/units/' + unit + '/replay?tag=' + tagged.tag);
    assert.equal(replay.commit, commit);
    assert.deepEqual(replay.checks.map(({ id, st }) => [id, st]), checkIds.map((id) => [id, 'pass']));
    const decisions = replay.facts.filter(({ type }) => type === 'check.decided');
    assert.deepEqual(decisions.map(({ actor }) => actor), checkIds.map(() => 'BA'));
    const tagFact = replay.facts.find(({ type }) => type === 'tag.created');
    assert.equal(tagFact.actor, 'SA');
    assert.ok(decisions.every(({ seq }) => seq < tagFact.seq));

    const orchPath = '/api/projects/' + project + '/orch';
    const batch = await post(orchPath + '/release', 201, { env: 'SIT', mode: 'full', units: [unit] });
    assert.equal(batch.env, 'SIT');
    assert.equal(batch.by, 'SA');
    assert.equal(batch.status, 'active');
    assert.equal(batch.units.length, 1);
    const [deployed] = batch.units;
    assert.equal(deployed.id, unit);
    assert.equal(deployed.commit, commit);
    assert.equal(deployed.digest, digest);
    assert.equal(deployed.tag, tagged.tag);
    assert.equal(deployed.status, 'deployed');
    assert.ok(deployed.binary && deployed.url);
    assert.deepEqual((await get(orchPath)).batches.find(({ id }) => id === batch.id), batch);
    const delivered = await get(statePath);
    assert.ok(delivered.deploys.some((d) => d.tag === tagged.tag && d.commit === commit && d.url === deployed.url && d.status === 'success'));
    const { facts } = await get('/api/facts' + query);
    assert.ok(facts.some(({ type, actor, seq }) => type === 'deploy.succeeded' && actor === 'SA' && seq > tagFact.seq));

    const opened = await page.goto(sitPath, { waitUntil: 'domcontentloaded' });
    assert.equal(opened.status(), 200);
    await page.getByRole('heading', { name: '新建入库', exact: true }).waitFor();
    const sitHealth = await checkHealth(page, sitPath, commit, 'sit');
    await screenshot(page, '06-sit-health');
    assert.deepEqual(errors, []);
    const evidence = { unit, item, commit, digest, test: test.payload, checks: replay.checks, preview, testHealth, rejected, receipts: confirmed, batch, sitHealth };
    if (shots) writeFileSync(join(shots, 'material37.json'), JSON.stringify(evidence, null, 2) + '\n');
    console.log('PASS SA 发布与 SIT 服务健康', batch.id, tagged.tag, sitPath, digest);
    console.log('SUMMARY 5/5 业务验收通过');
  } finally {
    await browser.close();
  }
})().catch((error) => {
  console.error('FATAL', error);
  process.exitCode = 1;
});
