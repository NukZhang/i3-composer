// 截图：v2.6 的关键页面（编排工作台 / 质量报告 / 验收台 / 原型屏）。
// 用法：node shots_v26.cjs [BASE] [OUTDIR]
const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');
const CHROME = '/Users/nukzhang/Library/Caches/ms-playwright/chromium-1217/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing';
const BASE = process.argv[2] || 'http://127.0.0.1:8092';
const OUT = process.argv[3] || '/tmp/i3-v26-shots';

(async () => {
  fs.mkdirSync(OUT, { recursive: true });
  const browser = await chromium.launch({ executablePath: CHROME });
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  // 明主题截图：白底更适合放进文档与文章。
  await ctx.addInitScript(() => {
    try {
      localStorage.setItem('i3-theme', 'light');
    } catch {
      /* 忽略 */
    }
  });
  const page = await ctx.newPage();
  await page.goto(BASE + '/', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1800);

  const shot = async (name) => {
    await page.screenshot({ path: path.join(OUT, name + '.png') });
    console.log('shot ' + name);
  };

  // Agent 编排工作台
  await page.click('.rolebtn:has-text("DA")');
  await page.waitForTimeout(600);
  await page.click('.sidebar .trow.sub:has-text("交付质检 Agent")');
  await page.waitForTimeout(700);
  await page.click('.trow.sub2:has-text("构建")');
  await page.waitForTimeout(900);
  await shot('v26-studio');

  // 选中模型节点
  await page.locator('.as-li:has-text("合规判定")').click();
  await page.waitForTimeout(500);
  await shot('v26-studio-node');

  // 质量报告
  await page.click('.ptab:has-text("质量报告")');
  await page.waitForTimeout(900);
  await shot('v26-quality');

  // 验收台（BA）
  await page.click('.rolebtn:has-text("BA")');
  await page.waitForTimeout(600);
  await page.click('.trow.sub2:has-text("验证")');
  await page.waitForTimeout(900);
  await shot('v26-accept');

  // Agent 原型：质检复核
  await page.click('.trow.sub2:has-text("原型")');
  await page.waitForTimeout(900);
  await shot('v26-proto-qa');

  // 组合项：催缴业务台 + 合并门禁
  await page.click('.sidebar .trow.sub:has-text("智能催缴")');
  await page.waitForTimeout(800);
  await page.click('.trow.sub2:has-text("原型")');
  await page.waitForTimeout(900);
  await shot('v26-proto-dun');
  await page.click('.trow.sub2:has-text("构建")');
  await page.waitForTimeout(700);
  await page.click('.ptab:has-text("质量报告")');
  await page.waitForTimeout(900);
  await shot('v26-hybrid-quality');

  // Agent 工厂
  await page.click('.rolebtn.x');
  await page.waitForTimeout(800);
  await shot('v26-workers');

  await browser.close();
})().catch((e) => { console.error('FATAL', e); process.exit(2); });