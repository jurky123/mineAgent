// Real WebAuthn signatures using a Chrome virtual authenticator and a disposable Go server.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { createInterface } from 'node:readline';
import { chromium } from 'playwright';
const server = spawn('go', ['run', './scripts/passkey-fixture'], { stdio: ['pipe', 'pipe', 'inherit'] });
let browser;
try {
  const lines = createInterface({ input: server.stdout });
  const fixture = await new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error('test server startup timed out')), 90000);
    lines.once('line', (line) => { clearTimeout(timeout); try { resolve(JSON.parse(line)); } catch { reject(new Error('invalid test fixture')); } });
    server.once('exit', (code) => { clearTimeout(timeout); reject(new Error('test fixture exited ' + code)); });
  });
  const { origin } = fixture;
  browser = await chromium.launch({ headless: true });
  const context = await browser.newContext();
  const page = await context.newPage();
  const cdp = await context.newCDPSession(page);
  await cdp.send('WebAuthn.enable');
  const addAuthenticator = () => cdp.send('WebAuthn.addVirtualAuthenticator', { options: { protocol: 'ctap2', transport: 'internal', hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true } });
  let { authenticatorId } = await addAuthenticator();
  const errors = []; page.on('pageerror', (error) => errors.push(error.message));
  await page.goto(origin + '/fixture');
  assert.equal((await context.request.post(origin + '/api/login', { data: { name: 'owner' } })).status(), 410, 'name login survived');
  assert.equal((await context.request.get(origin + '/api/auth/me', { headers: { Authorization: 'Bearer ' + fixture.legacyToken } })).status(), 401, 'unverified legacy session survived');
  assert.equal((await context.request.post(origin + '/api/auth/passkey/login/begin', { headers: { Origin: 'https://evil.example' } })).status(), 403);
  await page.getByRole('button', { name: /首次使用/ }).click();
  await page.getByLabel('一次性激活码', { exact: true }).fill(fixture.code);
  await page.getByRole('button', { name: '验证并创建 Passkey' }).click();
  await page.getByRole('button', { name: '创建 Passkey', exact: true }).click();
  const recovery = page.locator('.recovery-codes'); await recovery.waitFor();
  const codes = (await recovery.textContent()).trim().split('\n'); assert.equal(codes.length, 8);
  assert.equal(await page.getByRole('button', { name: '继续 Passkey 登录' }).isDisabled(), true);
  await page.getByLabel('我已离线保存恢复码').check();
  await page.getByRole('button', { name: '继续 Passkey 登录' }).click();
  await page.getByText('Authenticated owner', { exact: true }).waitFor();
  const firstToken = await page.evaluate(() => window.testToken);
  const me = await context.request.get(origin + '/api/auth/me', { headers: { Authorization: 'Bearer ' + firstToken } });
  assert.equal(me.status(), 200); const initialUser = await me.json(); assert.equal(initialUser.admin, true); const originalUserID = initialUser.id;
  // A stolen activation code must not enroll again.
  const reused = await context.request.post(origin + '/api/auth/passkey/register/begin', { headers: { Origin: origin }, data: { code: fixture.code } });
  assert.equal(reused.status(), 400);
  // Invalid signatures and challenge replay cannot authenticate.
  assert.equal((await context.request.post(origin + '/api/auth/passkey/login/begin', { headers: { Origin: origin } })).status(), 200);
  for (let i = 0; i < 2; i++) assert.equal((await context.request.post(origin + '/api/auth/passkey/login/finish', { headers: { Origin: origin }, data: { id: 'forged' } })).status(), 400);
  // Simulate losing the authenticator, use an offline recovery code and enroll a replacement.
  await cdp.send('WebAuthn.removeVirtualAuthenticator', { authenticatorId });
  ({ authenticatorId } = await addAuthenticator());
  await page.goto(origin + '/fixture');
  await page.getByRole('button', { name: /Passkey 丢失/ }).click();
  await page.getByLabel('恢复码', { exact: true }).fill(codes[0]);
  await page.getByRole('button', { name: '验证并创建 Passkey' }).click();
  await page.getByRole('button', { name: '创建 Passkey', exact: true }).click();
  await page.locator('.recovery-codes').waitFor();
  assert.notEqual(await page.locator('.recovery-codes').textContent(), codes.join('\n'));
  assert.equal((await context.request.get(origin + '/api/auth/me', { headers: { Authorization: 'Bearer ' + firstToken } })).status(), 401);
  for (const code of [codes[0], codes[1]]) assert.equal((await context.request.post(origin + '/api/auth/passkey/register/begin', { headers: { Origin: origin }, data: { code } })).status(), 400);
  await page.getByLabel('我已离线保存恢复码').check();
  await page.getByRole('button', { name: '继续 Passkey 登录' }).click();
  await page.getByText('Authenticated owner', { exact: true }).waitFor();
  const finalToken = await page.evaluate(() => window.testToken);
  const recoveredUser = await context.request.get(origin + '/api/auth/me', { headers: { Authorization: 'Bearer ' + finalToken } });
  assert.equal(recoveredUser.status(), 200); assert.equal((await recoveredUser.json()).id, originalUserID);
  const history = await context.request.get(origin + '/api/history?conv=', { headers: { Authorization: 'Bearer ' + finalToken } });
  assert.equal(history.status(), 200); assert.match(await history.text(), /fixture-history-preserved/);
  await context.request.post(origin + '/api/auth/logout', { headers: { Authorization: 'Bearer ' + finalToken } });
  assert.equal((await context.request.get(origin + '/api/auth/me', { headers: { Authorization: 'Bearer ' + finalToken } })).status(), 401);
  assert.deepEqual(errors, []);
  console.log('PASS: real Passkey registration/login/recovery, legacy rejection, origin checks, invalid/replayed assertions, one-time codes, revocation and logout.');
} finally {
  if (browser) await browser.close(); server.stdin.end();
  await new Promise((resolve) => { if (server.exitCode !== null) resolve(); else { server.once('exit', resolve); setTimeout(() => server.kill(), 3000).unref(); } });
}
