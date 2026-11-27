// 方案页的三个页签共用编辑器，预览与编辑区都应随容器宽度展开。
// 断言数：12（3 个页签 × 2 个宽度 × 预览/编辑区）。依赖顺序：无——只读方案页布局，不写账本。
// 与其它套件一样以 SUMMARY 收尾（以前只靠 assert 抛栈，跑挂了看不出「几过几挂」）。
const assert = require('node:assert/strict');
const { mkdirSync } = require('node:fs');
const { join } = require('node:path');
const { chromium } = require('playwright');

const base = process.env.BASE || 'http://127.0.0.1:8080';
const shots = process.env.SHOTS;

let passed = 0;
let total = 0;

(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH });
  try {
    const page = await browser.newPage({ viewport: { width: 1475, height: 900 } });
    await page.addInitScript(() => localStorage.setItem('i3-theme', 'light'));
    await page.goto(base, { waitUntil: 'domcontentloaded' });
    await page.locator('.sidebar .trow').filter({ hasText: '收费与工单数字化' }).click();
    await page.locator('.sidebar .trow').filter({ hasText: '收费缴费' }).click();
    await page.getByRole('button', { name: '隐藏右栏', exact: true }).click();

    if (shots) mkdirSync(shots, { recursive: true });
    for (const width of [1475, 1050]) {
      await page.setViewportSize({ width, height: 900 });
      for (const artifact of ['PRD', 'AD', 'AT']) {
        await page.locator('.ptabs').getByText(artifact, { exact: true }).click();
        await page.getByRole('heading', { name: artifact + ' · 文档', exact: true }).waitFor();
        await page.waitForFunction(() => document.querySelector('.md-edit')?.value.trim());
        const widths = await page.locator('.dc').evaluate((element) => ({
          shell: element.clientWidth,
          preview: element.querySelector('.dc-scroll').clientWidth,
          editor: element.querySelector('.md-edit').clientWidth,
        }));
        if (shots) await page.screenshot({ path: join(shots, artifact + '-' + width + '.png') });
        for (const panel of ['preview', 'editor']) {
          total += 1;
          assert.ok(
            Math.abs(widths.shell - widths[panel]) <= 2,
            artifact + ' @ ' + width + ' 的 ' + panel + ' 宽度 ' + widths[panel] + 'px，应填满 ' + widths.shell + 'px 的文档容器',
          );
          passed += 1;
        }
        console.log('PASS ' + artifact + ' @ ' + width + 'px，预览和编辑区使用完整容器宽度');
      }
    }
  } finally {
    await browser.close();
  }
  console.log('SUMMARY ' + passed + '/' + total + ' passed');
  if (passed !== total) process.exitCode = 1;
})().catch((error) => {
  console.error('FAIL ' + error.message);
  console.log('SUMMARY ' + passed + '/' + total + ' passed');
  process.exitCode = 1;
});
