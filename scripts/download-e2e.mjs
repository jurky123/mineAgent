// Real handlers, disposable SQLite/files, localhost only. Does not read runtime configuration.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { createInterface } from 'node:readline';
import { mkdir, readFile } from 'node:fs/promises';
import { chromium } from 'playwright';

const server = spawn('go', ['run', './scripts/passkey-fixture'], { stdio: ['pipe', 'pipe', 'inherit'] });
let browser;
try {
  const lines = createInterface({ input: server.stdout });
  const { origin } = await new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error('fixture startup timed out')), 90000);
    lines.once('line', (line) => { clearTimeout(timeout); resolve(JSON.parse(line)); });
    server.once('exit', () => { clearTimeout(timeout); reject(new Error('fixture exited')); });
  });
  browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  context.setDefaultTimeout(15000);
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await page.goto(origin + '/downloads');
  await page.getByLabel('账号名称', { exact: true }).fill('owner');
  await page.getByRole('button', { name: '继续', exact: true }).click();
  await page.getByLabel('创建密码', { exact: true }).fill('correct test phrase!');
  await page.getByLabel('再次输入密码', { exact: true }).fill('correct test phrase!');
  await page.getByRole('button', { name: '设置密码并进入' }).click();
  await page.locator('#download-admin').waitFor({ state: 'visible' });
  const contents = Buffer.from('disposable fixture tool bytes\n');
  await page.locator('#upload-file').setInputFiles({ name: '测试工具.bin', mimeType: 'application/octet-stream', buffer: contents });
  await page.getByRole('button', { name: '上传并生成链接' }).click();
  await page.locator('#new-link').waitFor({ state: 'visible' });
  const link = await page.locator('#share-link').inputValue();
  assert.ok(link.startsWith(origin + '/download#'));
  await page.getByText('下载请求 0 次 · 传输完成 0 次', { exact: true }).waitFor();
  await mkdir('/tmp/zkun-download-ui', { recursive: true });
  await page.screenshot({ path: '/tmp/zkun-download-ui/admin-desktop.png', fullPage: true });

  // Recipient has no account, cookies or login token. Reading metadata must not count.
  const recipient = await browser.newContext({ viewport: { width: 390, height: 844 }, acceptDownloads: true });
  recipient.setDefaultTimeout(15000);
  const downloadPage = await recipient.newPage();
  downloadPage.on('pageerror', (error) => errors.push(error.message));
  await downloadPage.goto(link);
  await downloadPage.getByRole('heading', { name: '测试工具.bin' }).waitFor();
  assert.equal(await downloadPage.locator('.login-view').count(), 0);
  assert.ok(await downloadPage.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
  await downloadPage.screenshot({ path: '/tmp/zkun-download-ui/recipient-mobile.png', fullPage: true });
  for (let i = 0; i < 2; i++) {
    const pending = downloadPage.waitForEvent('download');
    const responsePending = downloadPage.waitForResponse((response) => response.url().endsWith('/api/downloads/file'));
    await downloadPage.getByRole('button', { name: '下载文件', exact: true }).click();
    assert.equal((await responsePending).status(), 200, 'native download response status');
    const download = await pending;
    assert.equal(download.suggestedFilename(), '测试工具.bin');
    assert.deepEqual(await readFile(await download.path()), contents);
  }
  await page.getByRole('button', { name: '刷新', exact: true }).click();
  await page.getByText('下载请求 2 次 · 传输完成 2 次', { exact: true }).waitFor();
  await page.getByRole('button', { name: '查看记录' }).click();
  await page.locator('#events-body tr').first().waitFor();
  assert.equal(await page.locator('#events-body tr').count(), 2);
  assert.match(await page.locator('#events-body').textContent(), /127\.0\.0\.1/);
  await page.setViewportSize({ width: 390, height: 844 });
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
  await page.screenshot({ path: '/tmp/zkun-download-ui/admin-mobile.png', fullPage: true });

  await page.getByRole('button', { name: '停用链接', exact: true }).click();
  await page.getByRole('button', { name: '停用', exact: true }).click();
  await page.getByText('链接已停用', { exact: false }).first().waitFor();
  await downloadPage.reload();
  await downloadPage.getByText('链接无效或已停用', { exact: true }).waitFor();
  assert.equal(await downloadPage.locator('#download-form').isVisible(), false);
  await page.getByRole('button', { name: '生成新链接', exact: true }).click();
  await page.getByRole('button', { name: '生成', exact: true }).click();
  await page.locator('#new-link').waitFor({ state: 'visible' });
  const next = await page.locator('#share-link').inputValue();
  assert.notEqual(next, link);
  await downloadPage.goto(next);
  await downloadPage.getByRole('heading', { name: '测试工具.bin' }).waitFor();
  assert.deepEqual(errors, []);
  console.log('PASS: admin upload, anonymous native download, exact byte/name preservation, request/completion counts, IP records, revocation/rotation, desktop and mobile layout.');
} finally {
  if (browser) await browser.close();
  server.stdin.end();
  await new Promise((resolve) => {
    if (server.exitCode !== null) resolve();
    else { server.once('exit', resolve); setTimeout(() => server.kill(), 3000).unref(); }
  });
}
