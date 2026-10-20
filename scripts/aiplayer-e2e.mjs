// Disposable end-to-end checks. Never read MineAgent config, cookies or database.
import assert from "node:assert/strict";
import { mkdtemp, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import http from "node:http";
import net from "node:net";
import { spawn, spawnSync } from "node:child_process";
import { randomBytes } from "node:crypto";
import { chromium } from "playwright";

const binary = process.argv[2],
  writerBinary = process.argv[3];
assert(
  binary?.startsWith("/tmp/") && writerBinary?.startsWith("/tmp/"),
  "Pass disposable /tmp/aiplayerportal and /tmp/aiplayerwrite builds",
);
const dir = await mkdtemp(path.join(tmpdir(), "aiplayer-e2e-"));
const password = "test-" + randomBytes(20).toString("hex"),
  profileToken = randomBytes(32).toString("hex");
let server, browser, source, page;
let sourceStatus = 200,
  bodyStatus = "online",
  bodyActivity = "idle";
const errors = [];
let requestCount = 0;
async function port() {
  const s = net.createServer();
  await new Promise((r) => s.listen(0, "127.0.0.1", r));
  const p = s.address().port;
  await new Promise((r) => s.close(r));
  return p;
}
function cli(args) {
  const p = spawnSync(
    binary,
    ["--config", path.join(dir, "config.json"), ...args],
    { encoding: "utf8", timeout: 15000 },
  );
  assert.equal(p.status, 0, p.stderr);
  return p.stdout;
}
async function waitFor(fn) {
  let last;
  for (let i = 0; i < 100; i++) {
    try {
      const result = await fn();
      if (result) return result;
    } catch (e) {
      last = e;
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  throw last || new Error("readiness timed out");
}
try {
  source = http.createServer((req, res) => {
    requestCount++;
    assert.equal(req.url, "/api/body/profile");
    assert.equal(req.headers.authorization, "Bearer " + profileToken);
    if (sourceStatus !== 200) {
      res.writeHead(sourceStatus);
      res.end();
      return;
    }
    res.setHeader("Content-Type", "application/json");
    res.end(
      JSON.stringify({
        profileVersion: 1,
        id: "main",
        name: "MineAgentBot",
        displayName: "小智",
        status: bodyStatus,
        activity: bodyActivity,
        updatedAt: Date.now(),
        stale: false,
        onlineSince: Date.now() - 1000,
        capabilities: ["observe", "move"],
        position: { x: 12345 },
        lastEvent: "PRIVATE_EVENT_FIXTURE",
      }),
    );
  });
  await new Promise((r) => source.listen(0, "127.0.0.1", r));
  const listen = await port(),
    base = `http://127.0.0.1:${listen}`,
    config = {
      listen: `127.0.0.1:${listen}`,
      database: path.join(dir, "portal.db"),
      origin: base,
      name: "AI 玩家",
      forumEnabled: true,
      agents: [{ id: "main", name: "MineAgentBot", displayName: "小智" }],
      profile: {
        agentId: "main",
        url: `http://127.0.0.1:${source.address().port}/api/body/profile`,
        tokenFile: path.join(dir, "profile.token"),
      },
    };
  await writeFile(path.join(dir, "config.json"), JSON.stringify(config), {
    mode: 0o600,
  });
  await writeFile(path.join(dir, "password"), password, { mode: 0o600 });
  await writeFile(path.join(dir, "profile.token"), profileToken, {
    mode: 0o600,
  });
  cli([
    "--action",
    "admin",
    "--admin",
    "admin",
    "--password-file",
    path.join(dir, "password"),
  ]);
  cli([
    "--action",
    "issue-token",
    "--token-file",
    path.join(dir, "writer.token"),
  ]);
  server = spawn(binary, ["--config", path.join(dir, "config.json")], {
    stdio: ["ignore", "ignore", "pipe"],
  });
  server.stderr.on("data", () => {});
  await waitFor(async () => {
    const r = await fetch(base + "/healthz");
    return r.ok;
  });
  const request = {
    operation: "create",
    idempotencyKey: "e2e-initial-write",
    post: {
      title: "建筑日志 · 第一版",
      body: "# 建筑日志\n\n这是经过核对后公开的初稿。\n\n1. 准备材料\n2. 检查结构\n\n```js\nconst ready = true;\n```\n\n| 项目 | 结果 |\n| --- | --- |\n| 建筑 | 完成 |\n\n[参考](https://example.com)",
      tags: ["建筑"],
      lang: "zh-CN",
      status: "pending",
    },
  };
  const requestFile = path.join(dir, "request.json");
  await writeFile(requestFile, JSON.stringify(request), { mode: 0o600 });
  const write = () =>
    spawnSync(
      writerBinary,
      [
        "--base-url",
        base,
        "--token-file",
        path.join(dir, "writer.token"),
        "--request",
        requestFile,
      ],
      { encoding: "utf8", timeout: 15000 },
    );
  let submitted = write();
  assert.equal(submitted.status, 0, submitted.stderr);
  const created = JSON.parse(submitted.stdout);
  submitted = write();
  assert.equal(
    JSON.parse(submitted.stdout).id,
    created.id,
    "same client request duplicated post",
  );
  const publicList = await (await fetch(base + "/api/ai-posts")).json();
  assert.equal(publicList.total, 0);
  const profile = await (await fetch(base + "/api/ai-agents/main")).text();
  assert(!profile.includes("position"));
  assert(!profile.includes("PRIVATE_EVENT_FIXTURE"));
  assert(!profile.includes(profileToken));
  browser = await chromium.launch({ headless: true, args: ["--no-sandbox"] });
  const context = await browser.newContext({
    viewport: { width: 1280, height: 900 },
  });
  page = await context.newPage();
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("request", (r) => {
    assert.equal(new URL(r.url()).origin, base, "unexpected external request");
  });
  await page.goto(base + "/ai");
  await page
    .getByRole("heading", { name: "AI 玩家", forumEnabled: true, exact: true })
    .waitFor();
  await page.locator(".status.online").waitFor();
  await page.getByRole("link", { name: "查看资料 →" }).click();
  await page.getByRole("heading", { name: "公开文章 · 0" }).waitFor();
  await page.goto(base + "/ai/login");
  await page.getByLabel("管理员账号", { exact: true }).fill("admin");
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await page.getByRole("heading", { name: "内容管理", exact: true }).waitFor();
  await page
    .getByRole("heading", { name: "建筑日志 · 第一版" })
    .first()
    .waitFor();
  const check = page.getByRole("checkbox");
  await check.check();
  await page.getByRole("button", { name: "发布指定版本", exact: true }).click();
  await page.getByRole("dialog").waitFor();
  await page.keyboard.press("Escape");
  await page.getByRole("dialog").waitFor({ state: "detached" });
  assert(
    await page
      .getByRole("button", { name: "发布指定版本", exact: true })
      .evaluate((el) => el === document.activeElement),
    "dialog did not restore focus",
  );
  await page.getByRole("button", { name: "发布指定版本", exact: true }).click();
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "发布指定版本", exact: true })
    .click();
  await page.getByText("没有待审文章。", { exact: true }).waitFor();
  const live = await (await fetch(base + "/api/ai-posts/" + created.id)).json();
  assert.equal(live.title, "建筑日志 · 第一版");
  await page.goto(base + `/ai/main/post/${created.id}`);
  await page
    .getByRole("heading", { name: "建筑日志 · 第一版", exact: true })
    .waitFor();
  assert.equal(await page.locator("article table").count(), 1);
  assert.equal(await page.locator("article pre code").count(), 1);
  assert.equal(
    await page.locator('article a[target="_blank"]').getAttribute("rel"),
    "noopener noreferrer nofollow",
  );
  assert.equal(await page.locator("article img").count(), 0);
  await page.screenshot({
    path: path.join(dir, "published-desktop.png"),
    fullPage: true,
  });
  // Even a compromised read response cannot turn Markdown into executable HTML
  // or dangerous links. The normal write API independently rejects these inputs.
  const hostileRoute = "**/api/ai-posts/" + created.id;
  await page.route(hostileRoute, (route) =>
    route.fulfill({
      json: {
        ...live,
        body: '<img src="https://tracker.example/x" onerror="window.__xss=true">\n\n[x](javascript:window.__xss=true)\n\n[x](http://127.0.0.1/private)',
      },
    }),
  );
  await page.reload();
  await page
    .getByRole("heading", { name: "建筑日志 · 第一版", exact: true })
    .waitFor();
  assert.equal(
    await page
      .locator(
        'article img, article script, article a[href^="javascript:"], article a[href^="http://127."]',
      )
      .count(),
    0,
  );
  assert.equal(await page.evaluate(() => window.__xss), undefined);
  await page.unroute(hostileRoute);
  // Editing creates a new working revision while anonymous readers keep the live copy.
  // The production default is one author write/minute, including the controlled
  // editor. Do not add a test-only quota bypass to the service.
  await new Promise((r) => setTimeout(r, 61000));
  await page.goto(base + "/ai/new?post=" + created.id);
  await page.getByLabel("标题", { exact: true }).fill("建筑日志 · 待审第二版");
  await page
    .getByLabel("正文 · Markdown", { exact: true })
    .fill("新的待审正文");
  await page.getByRole("button", { name: "提交待审", exact: true }).click();
  await page
    .locator("main")
    .getByText(/已提交待审/)
    .waitFor();
  assert.equal(
    (await (await fetch(base + "/api/ai-posts/" + created.id)).json()).title,
    "建筑日志 · 第一版",
  );
  await page.goto(base + "/ai/moderation");
  await page.getByLabel("审核/操作理由", { exact: true }).fill("需要补充资料");
  await page.getByRole("button", { name: "拒绝", exact: true }).click();
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "拒绝", exact: true })
    .click();
  await page.getByText("没有待审文章。", { exact: true }).waitFor();
  assert.equal(
    (await (await fetch(base + "/api/ai-posts/" + created.id)).json()).title,
    "建筑日志 · 第一版",
  );
  await page.getByRole("link", { name: "全部文章", exact: true }).click();
  await page.getByRole("button", { name: "隐藏", exact: true }).click();
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "隐藏", exact: true })
    .click();
  await page.getByRole("button", { name: "恢复公开", exact: true }).waitFor();
  assert.equal((await fetch(base + "/api/ai-posts/" + created.id)).status, 404);
  assert.equal((await (await fetch(base + "/api/ai-posts")).json()).total, 0);
  await page.getByRole("button", { name: "恢复公开", exact: true }).click();
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "恢复公开", exact: true })
    .click();
  await page.getByRole("button", { name: "隐藏", exact: true }).waitFor();
  assert.equal((await fetch(base + "/api/ai-posts/" + created.id)).status, 200);
  await page.getByRole("button", { name: "删除", exact: true }).click();
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "删除", exact: true })
    .click();
  await page.getByRole("button", { name: "恢复为隐藏", exact: true }).waitFor();
  await page.getByRole("button", { name: "恢复为隐藏", exact: true }).click();
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "恢复为隐藏", exact: true })
    .click();
  await page
    .getByText("从删除恢复的文章必须重新审核，当前仍隐藏。", { exact: true })
    .waitFor();
  assert.equal(
    await page.getByRole("button", { name: "恢复公开", exact: true }).count(),
    0,
  );
  assert.equal((await fetch(base + "/api/ai-posts/" + created.id)).status, 404);
  // Admin JSON is protected after logout. Anonymous pages remain readable.
  await page.getByRole("link", { name: "退出管理", exact: true }).click();
  await page
    .getByRole("heading", { name: "AI 玩家", forumEnabled: true, exact: true })
    .waitFor();
  const privateStatus = await page.evaluate(
    async () => (await fetch("/api/ai-admin/posts")).status,
  );
  assert.equal(privateStatus, 401);
  for (const width of [390, 768, 1280]) {
    await page.setViewportSize({ width, height: 900 });
    await page.goto(base + "/ai/main");
    await page.getByRole("heading", { name: "公开文章 · 0" }).waitFor();
    assert(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
      `horizontal overflow at ${width}`,
    );
    await page.evaluate(() => scrollTo(0, 0));
    await page.screenshot({
      path: path.join(dir, `profile-${width}.png`),
      fullPage: true,
    });
  }
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.getByRole("button", { name: "切换深浅主题" }).click();
  assert.equal(await page.locator("html").getAttribute("data-theme"), "dark");
  await page.screenshot({
    path: path.join(dir, "profile-dark.png"),
    fullPage: true,
  });
  await page.evaluate(() => {
    document.body.style.zoom = "2";
  });
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
    "200% CSS zoom overflow",
  );
  await page.evaluate(() => {
    document.body.style.zoom = "";
  });
  // Stale on-screen online labels expire even when subsequent browser API
  // requests are blocked; HTTP errors are never presented as known offline.
  await page.route("**/api/ai-agents/**", (route) => route.abort());
  await page.locator(".status.unknown").waitFor({ timeout: 16000 });
  await page.unroute("**/api/ai-agents/**");
  bodyActivity = "moving";
  await page
    .locator('[data-live="activity"]')
    .filter({ hasText: "移动中" })
    .waitFor({ timeout: 16000 });
  bodyStatus = "offline";
  await waitFor(async () => {
    const p = await (await fetch(base + "/api/ai-agents/main")).json();
    return p.profile.status === "offline";
  });
  await page.reload();
  await page.getByText("离线", { exact: true }).waitFor();
  sourceStatus = 503;
  await waitFor(async () => {
    const p = await (await fetch(base + "/api/ai-agents/main")).json();
    return p.profile.status === "unknown";
  });
  await page.reload();
  await page.getByText("状态未知", { exact: true }).waitFor();
  sourceStatus = 200;
  bodyStatus = "online";
  await waitFor(async () => {
    const p = await (await fetch(base + "/api/ai-agents/main")).json();
    return p.profile.status === "online";
  });
  assert.deepEqual(errors, []);
  assert(requestCount > 0);
  console.log(
    JSON.stringify({
      result: "passed",
      checks: [
        "controlled writer idempotency",
        "public/private projection",
        "admin login and moderation",
        "live/working isolation",
        "reject/hide/restore/delete review",
        "logout",
        "390/768/1280 no overflow",
        "dark and reduced motion",
        "offline/503/recovery",
      ],
      artifacts: dir,
    }),
  );
} catch (e) {
  console.error("Browser errors:", errors);
  if (page) {
    console.error(
      "Main:",
      (await page.locator("main").innerText()).slice(0, 1500),
    );
    await page.screenshot({
      path: path.join(dir, "failure.png"),
      fullPage: true,
    });
  }
  throw e;
} finally {
  if (browser) await browser.close();
  if (server) {
    server.kill("SIGTERM");
    await new Promise((r) => {
      if (server.exitCode !== null) return r();
      server.once("exit", r);
    });
  }
  if (source) await new Promise((r) => source.close(r));
  /* Keep only screenshots; remove disposable DB/config/credentials. */ for (const name of [
    "portal.db",
    "portal.db-wal",
    "portal.db-shm",
    "config.json",
    "password",
    "profile.token",
    "writer.token",
    "request.json",
  ])
    await rm(path.join(dir, name), { force: true });
}
