// ui-preview.mjs — UI 预览与动效录制（开发工具，不参与服务端构建/部署）
//
// 用途：给 UI 评审固定一套「状态截图 + 动效视频」，改 CSS 后可重跑做前后对比（视觉回归）。
//
// 一次性准备：
//   npm install                                  # 安装 playwright（devDependency）
//   npx playwright install chromium              # 下载 Chromium（含自带 ffmpeg）
// 运行：
//   npm run ui-preview                           # 默认打 http://127.0.0.1:8877（本地测试实例）
//   node scripts/ui-preview.mjs --base https://zkun.art
//   node scripts/ui-preview.mjs --only motion    # 只录动效视频
//   node scripts/ui-preview.mjs --only shots     # 只截状态图
//
// 产出：ui-preview/
//   desktop/{portal,games,chess-lobby,chess-playing,account}.png       1440x900
//   mobile/{portal,games,chess-playing,account}.png                    390x844
//   states/{chess-check,chess-finished-win,chess-undo,gomoku-playing}.png
//   motion/{full-flow.webm,go…}

import { chromium } from 'playwright';
import { mkdir, rm, rename, readdir } from 'node:fs/promises';
import { existsSync } from 'node:fs';
import path from 'node:path';

const args = process.argv.slice(2);
function arg(name, def) {
  const i = args.indexOf('--' + name);
  return i >= 0 && args[i + 1] ? args[i + 1] : def;
}
const BASE = arg('base', 'http://127.0.0.1:8877').replace(/\/$/, '');
const OUT = arg('out', 'ui-preview');
const ONLY = arg('only', ''); // shots | motion | ''
const DESKTOP = { width: 1440, height: 900 };
const MOBILE = { width: 390, height: 844 };

const shots = [
  { dir: 'desktop', name: 'portal', url: '/?ui=1' },
  { dir: 'desktop', name: 'games', url: '/games?ui=1' },
  { dir: 'desktop', name: 'chess-lobby', url: '/games/chess?ui=1&state=lobby' },
  { dir: 'desktop', name: 'chess-playing', url: '/games/chess?ui=1&state=playing' },
  { dir: 'desktop', name: 'account', url: '/account?ui=1' },
  { dir: 'mobile', name: 'portal', url: '/?ui=1' },
  { dir: 'mobile', name: 'games', url: '/games?ui=1' },
  { dir: 'mobile', name: 'chess-playing', url: '/games/chess?ui=1&state=playing' },
  { dir: 'mobile', name: 'account', url: '/account?ui=1' },
  // 固定状态（轻量 Storybook：以后评审同一状态可直接对比）
  { dir: 'states', name: 'chess-check', url: '/games/chess?ui=1&state=check' },
  { dir: 'states', name: 'chess-finished-win', url: '/games/chess?ui=1&state=finished-win' },
  { dir: 'states', name: 'chess-undo', url: '/games/chess?ui=1&state=undo-request' },
  { dir: 'states', name: 'gomoku-playing', url: '/games/gomoku?ui=1&state=playing' },
  { dir: 'states', name: 'gomoku-finished', url: '/games/gomoku?ui=1&state=finished-win' },
  { dir: 'states', name: 'portal-dark', url: '/?ui=1&theme=dark' },
];

async function shoot(browser) {
  for (const s of shots) {
    const viewport = s.dir === 'mobile' ? MOBILE : DESKTOP;
    const ctx = await browser.newContext({ viewport, deviceScaleFactor: 1, reducedMotion: 'no-preference' });
    const page = await ctx.newPage();
    await page.goto(BASE + s.url, { waitUntil: 'domcontentloaded' }).catch(() => {});
    await page.waitForTimeout(1200); // 等假数据渲染 + 动效落定
    const file = path.join(OUT, s.dir, s.name + '.png');
    await page.screenshot({ path: file });
    console.log('shot', file);
    await ctx.close();
  }
}

// 连续流程录像：Portal（切主题/账号菜单）→ Games → Chess 大厅 → 等待 → 对局 →
// 落子 → 悔棋请求 → 终局；最后再录一段五子棋对局。
async function motion(browser) {
  const dir = path.join(OUT, 'motion');
  const ctx = await browser.newContext({ viewport: DESKTOP, recordVideo: { dir, size: DESKTOP } });
  const page = await ctx.newPage();
  const t = (ms) => page.waitForTimeout(ms);

  await page.goto(BASE + '/?ui=1', { waitUntil: 'domcontentloaded' });
  await t(1500);
  await page.click('.topbar .icon-btn');            // 主题选择器
  await t(900);
  await page.click('.topbar .pop-item[data-mode="dark"]');
  await t(1200);
  await page.click('.topbar .pop-item[data-mode="light"]');
  await t(600);
  await page.click('body');
  await page.click('.account-btn').catch(() => {});
  await t(900);
  await page.click('body');
  await page.hover('.game-card').catch(() => {});
  await t(900);

  await page.goto(BASE + '/games?ui=1', { waitUntil: 'domcontentloaded' });
  await t(1300);
  await page.hover('.catalog-card').catch(() => {});
  await t(700);

  // 棋局：motion 模式自己按时间线演示（12 秒）
  await page.goto(BASE + '/games/chess?ui=motion', { waitUntil: 'domcontentloaded' });
  await t(12500);
  const flowVideo = await page.video().path();
  await page.close();
  await ctx.close();
  await rename(flowVideo, path.join(dir, 'full-flow.webm'));
  console.log('video', path.join(dir, 'full-flow.webm'));

  // 五子棋一局
  const ctx2 = await browser.newContext({ viewport: DESKTOP, recordVideo: { dir, size: DESKTOP } });
  const page2 = await ctx2.newPage();
  await page2.goto(BASE + '/games/gomoku?ui=motion', { waitUntil: 'domcontentloaded' });
  await page2.waitForTimeout(12500);
  const gomokuVideo = await page2.video().path();
  await page2.close();
  await ctx2.close();
  await rename(gomokuVideo, path.join(dir, 'gomoku-flow.webm'));
  console.log('video', path.join(dir, 'gomoku-flow.webm'));
}

(async () => {
  if (existsSync(OUT)) await rm(OUT, { recursive: true, force: true });
  for (const d of ['desktop', 'mobile', 'states', 'motion']) {
    await mkdir(path.join(OUT, d), { recursive: true });
  }
  const browser = await chromium.launch();
  try {
    if (ONLY !== 'motion') await shoot(browser);
    if (ONLY !== 'shots') await motion(browser);
  } finally {
    await browser.close();
  }
  console.log('done →', OUT);
})();
