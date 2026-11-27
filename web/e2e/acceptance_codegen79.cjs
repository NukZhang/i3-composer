// #79：从访客工作项走真实 Dapr 交付，核对制品与测试证据的范围。
const assert = require('node:assert/strict');
const { mkdirSync } = require('node:fs');
const { join } = require('node:path');
const { setTimeout: delay } = require('node:timers/promises');
const { chromium } = require('playwright');
const { requirePubsub } = require('./require_pubsub.cjs');

const base = process.env.BASE || 'http://127.0.0.1:8080';
const run = process.env.RUN_TAG || Date.now().toString(36);
const unit = 'visitor-' + run.toLowerCase().replace(/[^a-z0-9]/g, '').slice(0, 12);
const query = '?pbc=' + unit;
const title = '当访客预约到访则应生成一次性通行凭证';
const criterion = '当访客预约到访，则应生成一次性通行凭证并在过期后失效';

(async () => {
  await requirePubsub(base);
  const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH });
  try {
    const page = await browser.newPage({ baseURL: base, viewport: { width: 1440, height: 960 } });
    await page.addInitScript(() => localStorage.setItem('i3-theme', 'light'));
    const get = (path) => request('GET', path, 200);
    const post = (path, status, data) => request('POST', path, status, data);
    async function request(method, path, status, data) {
      const response = await page.request.fetch(path, { method, data });
      const json = await response.json();
      assert.equal(response.status(), status, path + ' ' + JSON.stringify(json));
      return json;
    }
    const role = (name) => post('/api/role', 200, { role: name });
    const shot = async (name) => {
      if (!process.env.SHOTS) return;
      mkdirSync(process.env.SHOTS, { recursive: true });
      await page.screenshot({ path: join(process.env.SHOTS, name) });
    };

    await role('PA');
    await post('/api/units', 201, { id: unit, name: '访客通行凭证', kind: 'pbc', proj: 'p1' });
    await role('BA');
    const item = await post('/api/tasks' + query, 201, {
      title, type: 'new', env: 'test', mode: 'fast',
      accept: [criterion], data: ['visitor.pass.expires_at'], acceptBy: 'BA 访客验收', complyBy: 'SA 通行合规',
    });
    await role('DA');
    const itemPath = '/api/items/' + item.id;
    await post(itemPath + '/run' + query, 202);

    let state;
    for (const deadline = Date.now() + 90000; Date.now() < deadline; await delay(500)) {
      state = await get('/api/state' + query);
      if (state.items[item.id]?.run === 'verified') break;
      assert.notEqual(state.items[item.id]?.run, 'blocked', JSON.stringify(state.items[item.id]));
    }
    assert.equal(state.items[item.id]?.run, 'verified', JSON.stringify(state.items[item.id]));
    const head = state.commits[0];
    const version = (artifact) => state.versions[artifact].find(({ v }) => v === head.tree[artifact]);
    const { md: at } = version('at');
    assert.match(at, /When：访客预约到访\s+- Then：应生成一次性通行凭证并在过期后失效/);
    for (const kind of ['prd', 'proto', 'ad', 'at', 'backlog', 'code']) {
      const artifact = version(kind);
      const body = kind === 'code' ? Object.values(artifact.files).join('\n') : artifact.md;
      assert.match(body, /模板占位，非本单元口径/, kind);
      assert.doesNotMatch(body, /滞纳金|lateFee|批量生成幂等|账期/, kind);
      if (['prd', 'at', 'backlog', 'code'].includes(kind)) assert.match(body, /过期后失效/, kind);
    }
    console.log('PASS 制品使用访客口径，明确标注占位', JSON.stringify({ unit, item: item.id, commit: head.no, tree: head.tree }));

    const { facts } = await get(itemPath + query);
    const test = facts.find((fact) => fact.type === 'test.run' && fact.payload.commit === head.no);
    const build = facts.find((fact) => fact.type === 'build.succeeded' && fact.payload.commit === head.no);
    assert.deepEqual([test.payload.id, test.payload.scope, test.payload.pass, test.payload.total], [item.id, 'scaffold', 1, 1]);
    assert.deepEqual([state.test.commit, state.test.scope], [head.no, test.payload.scope]);
    assert.equal(build.payload.digest, head.digest);
    assert.match(head.digest, /^sha256:[0-9a-f]{64}$/);
    console.log('PASS 同一提交的真实测试和构建证据', JSON.stringify({ testSeq: test.seq, test: test.payload, buildSeq: build.seq, digest: head.digest }));

    await role('SA');
    assert.equal((state.checks || []).length, 0);
    const denied = await post('/api/units/' + unit + '/tag', 400);
    assert.match(denied.error, /没有对应的人工验收清单/);
    assert.ok(!(await get('/api/facts' + query)).facts.some(({ type }) => type === 'tag.created'));
    console.log('PASS 生成草案没有成为人工清单，SA 打 tag 被拒绝');

    await page.goto('/');
    const project = page.locator('.sidebar .trow.sub').filter({ hasText: '收费与工单数字化' });
    await project.waitFor();
    const row = page.locator('.sidebar [data-unit="' + unit + '"]');
    if (!await row.isVisible()) await project.click();
    await row.click();
    await page.locator('.sidebar .trow.sub2').filter({ hasText: '构建' }).click();
    await page.locator('.ptab').filter({ hasText: '质量报告' }).click();
    await page.locator('.stage').getByText('脚手架检查通过', { exact: false }).waitFor();
    await page.locator('.stage').getByText('业务未验证。', { exact: false }).waitFor();
    await shot('issue-79-quality-scope.png');
    await page.locator('.sidebar .trow.sub2').filter({ hasText: '验证' }).click();
    await page.locator('.stage').getByText('当前提交仅通过脚手架检查，业务未验证。', { exact: true }).waitFor();
    assert.ok(await page.getByRole('button', { name: '打 tag 固化交付物' }).isDisabled());
    await shot('issue-79-verify-scope.png');
    console.log('PASS 质量报告和验证页都明确显示业务未验证');
    console.log('SUMMARY 4/4');
  } finally {
    await browser.close();
  }
})().catch((err) => {
  console.error(err);
  process.exitCode = 1;
});
