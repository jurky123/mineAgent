// 浏览器交互回归：只启动临时静态服务与模拟 API，不读取配置、不访问线上数据。
import assert from 'node:assert/strict';
import { readFile, mkdir } from 'node:fs/promises';
import { createServer } from 'node:http';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const staticRoot = path.join(root, 'internal/webui/static');
const templateSource = (await readFile(path.join(root, 'internal/prompts/templates.go'), 'utf8')).split('var legacyPersonas')[0];
const personas = [...templateSource.matchAll(/\{ID:\s*"([^"]+)",([\s\S]*?)Text:\s*`([\s\S]*?)`/g)]
 .map(([,id,fields,text])=>({id,text,name:fields.match(/Name:\s*"([^"]+)"/)[1],desc:fields.match(/Desc:\s*"([^"]+)"/)[1],example:fields.match(/Example:\s*"([^"]+)"/)?.[1] || '',builtin:true}));
assert.equal(personas.length, 8);
let data = {
  admin: true, myPersona: '', webPersona: 'default', personas,
  limits: { persona: 8000, capability: 20000, name: 40, desc: 200 },
  services: [['web', '网页聊天', '门户 Agent 对话'], ['qq', 'QQ 机器人', '私聊与群聊'], ['minecraft', 'Minecraft', '游戏内聊天助手']].map(([id, name, desc]) => ({
    id, name, desc, persona: id === 'minecraft' ? 'mc-veteran' : 'default', defaultPersona: id === 'minecraft' ? 'mc-veteran' : 'default',
    default: '当前渠道：' + name + '\n\n工作方式：\n先理解用户的目标，直接给有用的回答。\n实时信息以工具结果为准，不虚构执行结果。\n\n渠道规则：\n遵循可用工具的权限与审批要求。\n根据所选人设调整语气，保留渠道规定的格式与长度。', global: { enabled: false },
  })),
};
const posts = [];
let promptReads = 0;
let failNextSave = false;
const types = { '.html': 'text/html', '.js': 'text/javascript', '.mjs': 'text/javascript', '.css': 'text/css', '.svg': 'image/svg+xml', '.png': 'image/png', '.woff2': 'font/woff2' };
const server = createServer(async (req, res) => {
  try {
    const url = new URL(req.url, 'http://localhost');
    const json = (value, code = 200) => { res.writeHead(code, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(value)); };
    if (url.pathname === '/api/prompts') {
      if (req.method === 'POST') {
        let body = ''; for await (const chunk of req) body += chunk;
        const p = JSON.parse(body); posts.push(p);
        if (failNextSave) { failNextSave = false; return json({ error: '模拟保存失败' }, 500); }
        let id = p.id;
        if (p.scope === 'user-persona' || p.scope === 'user-settings') { data.myPersona = p.persona; data.style = p.style || {}; }
        if (p.scope === 'service-settings') {
          const svc = data.services.find((s) => s.id === p.id);
          svc.persona = p.persona || svc.defaultPersona;
          svc.global = { enabled: p.enabled, text: p.text };
          if (p.id === 'web') data.webPersona = svc.persona;
        }
        if (p.scope === 'persona-save') {
          id = p.data.id || 'p-preview';
          const old = data.personas.find((item) => item.id === id);
          const saved = { ...p.data, id, builtin: old?.builtin || false };
          data.personas = old ? data.personas.map((item) => item.id === id ? saved : item) : [...data.personas, saved];
        }
        if (p.scope === 'persona-delete') {
          const old = data.personas.find((item) => item.id === p.id);
          if (old.builtin) data.personas = data.personas.map((item) => item.id === p.id ? structuredClone(personas.find((b) => b.id === p.id)) : item);
          else {
            data.personas = data.personas.filter((item) => item.id !== p.id);
            if (data.myPersona === p.id) data.myPersona = '';
            for (const svc of data.services) if (svc.persona === p.id) svc.persona = svc.defaultPersona;
            data.webPersona = data.services.find((svc) => svc.id === 'web').persona;
          }
        }
        return json({ ok: true, id });
      }
      promptReads++; return json(data);
    }
    if (url.pathname === '/api/me' || url.pathname === '/api/auth/me') return json({ name: 'Preview', id: 1, admin: data.admin });
    if (url.pathname === '/api/version') return json({ version: 'preview' });
    if (url.pathname === '/api/conversations') return json({ conversations: [] });
    if (url.pathname === '/api/history') return json({ messages: [] });
    if (url.pathname === '/api/options') return json({ models: [], skills: [], effort: 'medium', admin: data.admin });
    if (url.pathname === '/api/events') {
      res.writeHead(200, { 'Content-Type': 'text/event-stream' }); res.write(': preview\n\n');
      req.on('close', () => res.end()); return;
    }
    if (url.pathname.startsWith('/api/')) return json({ sessions: [], games: [], apps: [] });
    const pages = { '/agent': 'chat/index.html', '/account': 'account/index.html', '/': 'portal/index.html' };
    const rel = pages[url.pathname] || url.pathname.replace(/^\/static\/preview\//, '');
    const file = path.resolve(staticRoot, rel);
    if (!file.startsWith(staticRoot + path.sep)) { res.writeHead(404); res.end(); return; }
    let content = await readFile(file);
    if (file.endsWith('.html')) content = Buffer.from(content.toString().replaceAll('__VER__', 'preview'));
    res.writeHead(200, { 'Content-Type': types[path.extname(file)] || 'application/octet-stream' }); res.end(content);
  } catch { res.writeHead(404); res.end(); }
});
await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
const base = 'http://127.0.0.1:' + server.address().port;
let browser;
const out = process.env.PROMPT_UI_OUT || '/tmp/mineagent-prompts-ui';
async function shot(page, name) {
  await page.locator('.agent-prompts').evaluate((el) => Promise.all(el.getAnimations({ subtree: true }).map((a) => a.finished.catch(() => {}))));
  await page.screenshot({ path: path.join(out, name + '.png') });
}
try {
  await mkdir(out, { recursive: true });
  browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1440, height: 960 } });
  await context.addInitScript(() => {
    localStorage.setItem('mineagent.token', 'preview'); localStorage.setItem('mineagent.name', 'Preview');
    localStorage.setItem('mineagent.theme', 'light');
  });
  const page = await context.newPage();
  const errors = []; page.on('pageerror', (e) => errors.push(e.message));
  await page.goto(base + '/agent');
  await page.locator('#header-prompts').click();
  await page.locator('.prompt-choice').first().waitFor();
  assert.equal(await page.getByRole('tab').count(), 3);
  assert.equal(await page.locator('.prompt-choice').count(), 9);
  await page.locator('.agent-prompts .dialog-head button').focus();
  await page.keyboard.press('Shift+Tab');
  assert.equal(await page.evaluate(() => document.activeElement.tagName), 'SUMMARY', 'focus trap missed preview summary');
  await page.keyboard.press('Tab');
  assert.equal(await page.evaluate(() => document.activeElement.getAttribute('aria-label')), '关闭', 'focus escaped the dialog');
  assert.equal(await page.evaluate(() => document.body.classList.contains('ds')), false, 'chat layout replaced by portal layout');
  await shot(page, 'desktop-personas');
  await page.locator('[data-persona=tech]').click();
  await page.locator('.prompt-full-text summary').click();
  assert.match(await page.locator('.prompt-preview').innerText(), /资深工程师搭档/);
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: '取消', exact: true }).click();
  await page.locator('.dialog-mask').nth(1).waitFor({ state: 'detached' });
  assert.equal(await page.locator('.agent-prompts').count(), 1, 'Escape closed parent editor');
  failNextSave = true;
  await page.getByRole('button', { name: '应用偏好', exact: true }).click();
  await page.getByRole('alert').filter({ hasText: '模拟保存失败' }).waitFor();
  assert.equal(data.myPersona, '', 'failed save changed settings');
  assert.equal(await page.locator('[data-persona=tech]').getAttribute('aria-checked'), 'true', 'failed save lost draft');
  await page.getByRole('button', { name: '应用偏好', exact: true }).click();
  await page.waitForFunction(() => document.querySelector('#agent-persona-label').textContent === '技术搭档');
  assert.equal(data.myPersona, 'tech');
  await page.getByRole('tab', { name: '渠道设置' }).click();
  assert.equal(await page.locator('#prompt-capability').getAttribute('readonly'), '');
  await page.getByRole('button', { name: '从内置开始编辑' }).click();
  await page.locator('#prompt-capability').fill('自定义渠道规则');
  await page.locator('.prompt-selector-item').filter({ hasText: 'QQ 机器人' }).click();
  await page.getByRole('button', { name: '取消', exact: true }).click();
  await page.locator('.dialog-mask').nth(1).waitFor({ state: 'detached' });
  assert.equal(await page.locator('#prompt-capability').inputValue(), '自定义渠道规则', 'channel switch lost unsaved editor text');
  await page.locator('#prompt-service-persona').selectOption('teacher');
  await page.getByRole('button', { name: '保存更改', exact: true }).click();
  await page.waitForFunction(() => document.querySelector('.prompt-footer .btn').disabled && !document.querySelector('.prompt-status').textContent.includes('正在保存'));
  assert.equal(posts.at(-1).scope, 'service-settings');
  assert.equal(data.services[0].persona, 'teacher');
  await shot(page, 'desktop-channel');
  await page.getByRole('tab', { name: '模板库' }).click();
  await page.getByRole('button', { name: '＋ 新建模板' }).click();
  await page.locator('#prompt-template-name').fill('友善的伙伴');
  await page.locator('#prompt-template-desc').fill('清晰表达，一起解决问题');
  await page.locator('#prompt-template-text').fill('你是一位耐心的伙伴，先听清目标，再给具体建议。');
  await page.getByRole('button', { name: '保存更改', exact: true }).click();
  await page.locator('.prompt-selector-item.selected').filter({ hasText: '友善的伙伴' }).waitFor();
  assert.equal(data.personas.at(-1).name, '友善的伙伴');
  await shot(page, 'desktop-templates');
  await page.getByRole('button', { name: '删除模板', exact: true }).click();
  await page.getByRole('button', { name: '删除', exact: true }).click();
  await page.locator('.prompt-selector-item').filter({ hasText: '友善的伙伴' }).waitFor({ state: 'detached' });
  assert(!data.personas.some((p) => p.id === 'p-preview'));
  await page.locator('.prompt-selector-item').filter({ hasText: '技术搭档' }).click();
  await page.locator('#prompt-template-text').fill('临时修改的模板正文');
  await page.getByRole('button', { name: '保存更改', exact: true }).click();
  await page.waitForFunction(() => document.querySelector('.prompt-footer .btn').disabled && !document.querySelector('.prompt-status').textContent.includes('正在保存'));
  await page.getByRole('button', { name: '恢复内置模板', exact: true }).click();
  await page.getByRole('button', { name: '恢复', exact: true }).click();
  await page.waitForFunction(() => document.querySelector('#prompt-template-text').value.includes('资深工程师搭档'));
  await page.locator('.agent-prompts .dialog-head button').click();
  await page.locator('.agent-prompts').waitFor({ state: 'detached' });
  assert.equal(await page.evaluate(() => document.activeElement.id), 'header-prompts', 'focus did not return to opener');
  await page.goto(base + '/account');
  assert.equal(await page.locator('#personas-rows, #prompts-admin-section').count(), 0, 'settings still on account page');
  assert.deepEqual(errors, []);
  console.log('PASS: selection, failed save, unsaved guards, channel save, template CRUD/reset, keyboard focus, account removal');
  await context.close();

  data.admin = false;
  for (const theme of ['light', 'dark']) {
    const mobile = await browser.newContext({ viewport: { width: 390, height: 844 } });
    await mobile.addInitScript((mode) => {
      localStorage.setItem('mineagent.token', 'preview'); localStorage.setItem('mineagent.name', 'Preview');
      localStorage.setItem('mineagent.theme', mode);
    }, theme);
    const p = await mobile.newPage();
    await p.goto(base + '/agent'); await p.locator('#header-prompts').click();
    await p.locator('.prompt-choice').first().waitFor();
    assert.equal(await p.getByRole('tab').count(), 1, 'non-admin sees administrative controls');
    const bounds = await p.locator('.agent-prompts').boundingBox();
    assert(bounds.x >= 0 && bounds.x + bounds.width <= 390 && bounds.height <= 844, 'mobile dialog overflows');
    assert.equal(await p.locator('.prompt-content').evaluate((el) => el.scrollWidth <= el.clientWidth + 1), true, 'mobile content scrolls horizontally');
    await shot(p, 'mobile-' + theme);
    const pick = theme === 'light' ? 'concise' : 'translator';
    const label = personas.find(p => p.id === pick).name;
    await p.locator('[data-persona=' + pick + ']').click();
    await p.getByRole('button', { name: '应用偏好', exact: true }).click();
    await p.waitForFunction((name) => document.querySelector('#agent-persona-label').textContent === name, label);
    await mobile.close();
  }
  console.log('PASS: mobile light/dark layouts and regular-user controls');
  const demo = await browser.newPage();
  const reads = promptReads, writes = posts.length;
  await demo.goto(base + '/agent?ui=1&panel=prompts');
  await demo.locator('.prompt-choice').first().waitFor();
  await demo.getByRole('tab', { name: '渠道设置' }).click();
  await demo.getByRole('button', { name: '从内置开始编辑' }).click();
  assert.equal(await demo.locator('.prompt-footer button').isVisible(), false);
  assert.equal(promptReads, reads, 'debug preview read real prompt settings');
  assert.equal(posts.length, writes, 'debug preview wrote prompt settings');
  await demo.close();
  console.log('PASS: UI preview is isolated from prompt APIs');
  console.log('Screenshots:', out);
} finally {
  if (browser) await browser.close();
  server.closeAllConnections(); await new Promise((resolve) => server.close(resolve));
}
