// #77：用真实工作流验证备齐输入的修复项和需要补录的历史修复项。
const assert = require('node:assert/strict');
const { mkdirSync } = require('node:fs');
const { join } = require('node:path');
const { setTimeout: delay } = require('node:timers/promises');
const { chromium } = require('playwright');
const { requirePubsub } = require('./require_pubsub.cjs');

const base = process.env.BASE || 'http://127.0.0.1:8080';
const run = process.env.RUN_TAG || Date.now().toString(36);
const unit = 'repair-' + run.toLowerCase().replace(/[^a-z0-9]/g, '').slice(0, 14);
const acceptText = [
  '- AT-01 核对报告。Given 服务完成构建，When 查看质量报告，Then 返回测试通过数和摘要。',
  '- AT-02 核对列表。Given 当前账期已有样本，When 请求账单列表，Then 显示当前账期。',
].join('\n');

(async () => {
  await requirePubsub(base);
  const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH });
  try {
    const page = await browser.newPage({ baseURL: base, viewport: { width: 1460, height: 1050 } });
    await page.addInitScript(() => localStorage.setItem('i3-theme', 'light'));
    const request = async (method, path, status, data) => {
      const response = await page.request.fetch(path, { method, data });
      const json = await response.json();
      assert.equal(response.status(), status, path + ' ' + JSON.stringify(json));
      return json;
    };
    const post = (path, status, data) => request('POST', path, status, data);
    const get = (path) => request('GET', path, 200);
    const role = (name) => post('/api/role', 200, { role: name });
    const state = (id) => get('/api/state?pbc=' + id);
    const itemPath = (id, pbc) => '/api/items/' + id + '?pbc=' + pbc;
    const decide = (id, commit, pbc, data) =>
      post('/api/checks/' + id + '/decide?pbc=' + pbc, 201, { ...data, commit });
    const runItem = (id, pbc) => post('/api/items/' + id + '/run?pbc=' + pbc, 202);
    const verified = async (id, pbc) => {
      for (const deadline = Date.now() + 90000; Date.now() < deadline; await delay(500)) {
        const view = await state(pbc);
        const item = view.items[id];
        if (item.run === 'verified') {
          assert.equal(view.test.id, id);
          assert.equal(view.test.commit, item.version);
          return view;
        }
        assert.notEqual(item.run, 'blocked', JSON.stringify(item));
      }
      assert.fail('工作项未完成：' + id);
    };
    const verification = async (pbc) => {
      await page.goto('/');
      const project = page.locator('.sidebar .trow.sub').filter({ hasText: '收费与工单数字化' });
      await project.waitFor();
      const row = page.locator('.sidebar [data-unit="' + pbc + '"]');
      if (!await row.isVisible()) await project.click();
      await row.click();
      await page.locator('.sidebar .trow.sub2').filter({ hasText: '验证' }).click();
    };
    const screenshot = async (name) => {
      if (!process.env.SHOTS) return;
      mkdirSync(process.env.SHOTS, { recursive: true });
      await page.screenshot({ path: join(process.env.SHOTS, name) });
    };

    await role('PA');
    await post('/api/units', 201, { id: unit, name: '缺陷重验专项', kind: 'pbc', proj: 'p1' });
    await role('BA');
    await post('/api/artifacts?pbc=' + unit, 201, { artifact: 'at', text: acceptText });
    const source = await post('/api/tasks?pbc=' + unit, 201, {
      title: '核对当前账期和交付证据', type: 'new', env: 'test', mode: 'fast',
      accept: ['当查询账单列表时，则应显示当前账期'],
      data: ['fee.bill.amount'], acceptBy: '账单验收岗', complyBy: '账单合规岗',
    });
    await role('DA');
    await runItem(source.id, unit);
    const first = await verified(source.id, unit);
    const firstCommit = first.commits[0].no;
    await role('BA');
    await decide('AT-01', firstCommit, unit, { st: 'pass' });
    const { defect } = await decide('AT-02', firstCommit, unit, { st: 'fail', title: '列表缺少当前账期', steps: '打开账单列表核对账期' });
    const id = defect.repairItem;
    const detail = await get(itemPath(id, unit));
    assert.equal(detail.item.repair.sourceItem, source.id);
    assert.match(detail.repairInputs.accept[0], /Given 当前账期已有样本.*When 请求账单列表.*Then 显示当前账期/);
    assert.deepEqual(detail.repairInputs.data, ['fee.bill.amount']);
    assert.equal(detail.repairInputs.acceptBy, '账单验收岗');
    assert.equal(detail.repairInputs.complyBy, '账单合规岗');
    assert.ok(!detail.repairInputs.reasons?.length);
    const before = await get('/api/facts?pbc=' + unit);
    assert.deepEqual(before.facts.slice(0, 3).map(({ type }) => type), ['item.created', 'defect.created', 'check.decided']);
    assert.equal(before.facts[2].seq, detail.item.repair.decisionSeq);
    console.log('PASS 验收失败、缺陷和修复项连续落账，修复项带原验收与可追溯准入', JSON.stringify({
      pbc: unit, id, defect: defect.id, source: source.id, facts: before.facts.slice(0, 3),
    }));

    await verification(unit);
    await page.getByRole('button', { name: '修复项 ' + id, exact: true }).click();
    await page.getByRole('heading', { name: '缺陷 ' + defect.id + ' · 验收项 AT-02', exact: true }).waitFor();
    await page.getByRole('button', { name: 'DA', exact: true }).click();
    const [started] = await Promise.all([
      page.waitForResponse((res) => res.url().endsWith('/api/items/' + id + '/run?pbc=' + unit) && res.request().method() === 'POST'),
      page.getByRole('button', { name: '发起直通', exact: true }).click(),
    ]);
    assert.equal(started.status(), 202);
    const fixed = await verified(id, unit);
    const newCommit = fixed.commits[0].no;
    assert.notEqual(newCommit, firstCommit);
    assert.equal(fixed.checkCommit, newCommit);
    assert.deepEqual(fixed.checks.map(({ id, st, retestItem }) => [id, st, retestItem]), [
      ['AT-01', 'todo', undefined], ['AT-02', 'todo', id],
    ]);
    assert.ok(fixed.items[id].admit.st === 'admitted');
    await page.getByRole('button', { name: '前往验证', exact: true }).click();
    await page.locator('.ac-chip.on').filter({ hasText: 'AT-02' }).filter({ hasText: '待重验' }).waitFor();
    await screenshot('issue77-retest.png');
    console.log('PASS DA 直接发起，机器交付后当前提交仅对关联验收项标记待重验', JSON.stringify({
      pbc: unit, id, old: firstCommit, commit: newCommit, checks: fixed.checks, test: fixed.test,
    }));

    await role('SA');
    const blocked = await post('/api/units/' + unit + '/tag', 400);
    assert.match(blocked.error, /还差 2 条/);
    await role('BA');
    for (const check of fixed.checks) await decide(check.id, newCommit, unit, { st: 'pass' });
    assert.ok((await state(unit)).checks.every((check) => check.st === 'pass' && !check.retestItem));
    await role('SA');
    const tag = await post('/api/units/' + unit + '/tag', 201);
    assert.equal(tag.commit, newCommit);
    console.log('PASS 重验前打 tag 返回 400，BA 完成当前提交验收后可打 tag', JSON.stringify({ blocked, tag }));

    await role('BA');
    const legacy = await state('dun');
    const failingCommit = legacy.commits[0].no;
    const { defect: legacyDefect } = await decide('DR-4', failingCommit, 'dun', { st: 'fail', title: '第四次触达应被拦截' });
    const legacyID = legacyDefect.repairItem;
    const { defect: nextDefect } = await decide('DR-5', failingCommit, 'dun', { st: 'fail', title: '草稿出现减免承诺' });
    const legacyDetail = await get(itemPath(legacyID, 'dun'));
    assert.deepEqual(legacyDetail.repairInputs.reasons.map((r) => r.k), ['data', 'owner']);
    await verification('dun');
    await page.getByRole('button', { name: '修复项 ' + legacyID, exact: true }).click();
    await page.getByText('发起修复前需补准入输入', { exact: true }).waitFor();
    await page.getByRole('button', { name: '补输入', exact: true }).click();
    assert.match(await page.getByLabel(/验收口径/).inputValue(), /Then 第 4 次被拦下并提示/);
    await screenshot('issue77-repair-inputs.png');
    await page.getByLabel(/数据范围/).fill('dun.contact.attempt_count');
    await page.getByLabel('验收人', { exact: true }).fill('催缴验收岗');
    await page.getByLabel('合规背责人', { exact: true }).fill('催缴合规岗');
    const [filled] = await Promise.all([
      page.waitForResponse((res) => res.url().endsWith('/api/items/' + legacyID + '/inputs?pbc=dun') && res.request().method() === 'POST'),
      page.getByRole('button', { name: '补进账本', exact: true }).click(),
    ]);
    assert.equal(filled.status(), 200);
    await role('DA');
    await runItem(legacyID, 'dun');
    const legacyFixed = await verified(legacyID, 'dun');
    assert.notEqual(legacyFixed.checkCommit, failingCommit);
    assert.equal(legacyFixed.checkCommit, legacyFixed.commits[0].no);
    assert.equal(legacyFixed.checks.find((check) => check.id === 'DR-4').retestItem, legacyID);
    assert.ok(legacyFixed.checks.every((check) => check.st === 'todo'));
    console.log('PASS 历史检查缺少准入数据可先补录，修复后绑定新提交并重验原清单', JSON.stringify({
      pbc: 'dun', legacyID, old: failingCommit, commit: legacyFixed.checkCommit,
      missing: legacyDetail.repairInputs.reasons.map((r) => r.k),
      retest: legacyFixed.checks.find((check) => check.id === 'DR-4'),
    }));
    await role('BA');
    await post('/api/items/' + nextDefect.repairItem + '/inputs?pbc=dun', 200, {
      data: ['dun.contact.draft'], acceptBy: '催缴验收岗', complyBy: '催缴合规岗',
    });
    await role('DA');
    await runItem(nextDefect.repairItem, 'dun');
    const nextFixed = await verified(nextDefect.repairItem, 'dun');
    assert.notEqual(nextFixed.checkCommit, legacyFixed.checkCommit);
    assert.equal(nextFixed.checkCommit, nextFixed.commits[0].no);
    assert.equal(nextFixed.checks.length, legacy.checks.length);
    assert.equal(nextFixed.checks.find((check) => check.id === 'DR-5').retestItem, nextDefect.repairItem);
    console.log('PASS 同一旧提交登记的第二个修复项接续当前清单', JSON.stringify({
      pbc: 'dun', id: nextDefect.repairItem, commit: nextFixed.checkCommit,
      retest: nextFixed.checks.find((check) => check.id === 'DR-5'),
    }));
    await role('BA');
    const failingRepair = await decide('AT-02', tag.commit, unit, { st: 'fail', title: '验证机器测试受阻记录' });
    await role('DA');
    const built = await state(unit);
    const code = built.versions.code.find((v) => v.v === built.commits[0].tree.code);
    await post('/api/artifacts?pbc=' + unit, 201, {
      artifact: 'code',
      files: {
        ...code.files,
        'repair_failure_test.go': 'package main\nimport "testing"\nfunc TestRepairFailure(t *testing.T) { t.Fatal("专项验证受阻") }\n',
      },
    });
    const failingID = failingRepair.defect.repairItem;
    await runItem(failingID, unit);
    let failed;
    for (const deadline = Date.now() + 90000; Date.now() < deadline; await delay(500)) {
      failed = await state(unit);
      if (failed.items[failingID].run === 'blocked') break;
    }
    assert.equal(failed.items[failingID].run, 'blocked');
    assert.match(failed.items[failingID].blocked, /测试未通过：TestRepairFailure/);
    assert.ok(failed.checks.every((check) => !check.retestItem));
    const failureFacts = (await get(itemPath(failingID, unit))).facts;
    const blockedFact = failureFacts.find((fact) => fact.type === 'item.blocked');
    assert.match(blockedFact?.payload.reason, /测试未通过：TestRepairFailure/);
    assert.ok(!failureFacts.some((fact) => ['item.verified', 'build.succeeded'].includes(fact.type)));
    console.log('PASS 机器测试失败落受阻事实，验收项不被标记为待重验', JSON.stringify({
      id: failingID, run: failed.items[failingID].run, blockedFact,
    }));
    console.log('SUMMARY 6/6 缺陷修复专项通过');
  } finally {
    await browser.close();
  }
})().catch((error) => { console.error('FATAL', error); process.exitCode = 1; });
