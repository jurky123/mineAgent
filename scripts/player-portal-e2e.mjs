// Real portal/independent service integration with temporary identities/databases
// and simulated Node/Paper peers. Does not read or connect to production.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { chromium } from "playwright";
const binary = process.argv[2];
assert(binary?.startsWith("/tmp/"), "Pass a /tmp/player-portal-fixture build");
const fixture = spawn(binary, [], { stdio: ["pipe", "pipe", "pipe"] });
let browser;
const errors = [];
const artifacts = await mkdtemp(path.join(tmpdir(), "player-profile-ui-"));
try {
  const config = await new Promise((resolve, reject) => {
    let raw = "";
    fixture.stdout.on("data", (b) => {
      raw += b;
      const line = raw.split("\n")[0];
      try {
        resolve(JSON.parse(line));
      } catch {}
    });
    fixture.once("exit", () =>
      reject(new Error("fixture exited before ready")),
    );
    fixture.once("error", reject);
  });
  const { origin, adminToken, viewerToken, gameCode, publishingCookie } =
    config;
  assert.match(gameCode, /^[0-9a-f]{32}$/);
  browser = await chromium.launch({ headless: true, args: ["--no-sandbox"] });
  const context = await browser.newContext({
    viewport: { width: 1280, height: 900 },
  });
  const page = await context.newPage();
  page.on("pageerror", (e) =>
    errors.push({
      name: e.name,
      message: e.message,
      url: page.url(),
      stack: e.stack,
    }),
  );
  await page.goto(origin + "/");
  await page.getByRole("heading", { name: "zkun 门户" }).waitFor();
  assert.equal(
    await page
      .locator(
        '.topbar nav a[href="/ai"], .topbar nav a[href="/body"], .entry-grid a[href="/ai"], .entry-grid a[href="/body"]',
      )
      .count(),
    0,
  );
  await page.getByRole("link", { name: "下载与加入指南" }).click();
  await page.getByRole("heading", { name: "Minecraft", exact: true }).waitFor();
  await page.getByRole("link", { name: "查看玩家列表 →" }).click();
  await page.getByRole("heading", { name: "AI 玩家", exact: true }).waitFor();
  await page.evaluate(() => localStorage.setItem("mineagent.theme", "dark"));
  await page.reload();
  assert.equal(await page.locator("html").getAttribute("data-theme"), "dark");
  await page.getByRole("button", { name: "切换深浅主题" }).click();
  assert.equal(await page.evaluate(() => localStorage.getItem("mineagent.theme")), "light");
  await page.getByRole("link", { name: "跳到正文", exact: true }).focus();
  await page.keyboard.press("Enter");
  assert.equal(await page.evaluate(() => document.activeElement.id), "main");
  await page.screenshot({ path: path.join(artifacts, "directory-desktop.png"), fullPage: true });
  assert.equal(
    await page.getByRole("link", { name: "文章", exact: true }).count(),
    0,
  );
  assert.equal(
    await page.getByRole("link", { name: "管理登录", exact: true }).count(),
    0,
  );
  await page.locator('article a[href="/ai/main"]').click();
  await page.getByRole("heading", { name: "身体状态与控制" }).waitFor();
  await page.getByText(/需要主站已验证的管理员账号/).waitFor();
  assert.equal(await page.locator("#body-stop").count(), 0);
  assert(
    !(await page
      .locator("main")
      .innerText()
      .then((s) => s.includes("private-fixture-world"))),
  );
  const publicProfile = await page.evaluate(
    async () => await (await fetch("/api/ai-agents/main")).json(),
  );
  assert(!JSON.stringify(publicProfile).includes("private-fixture-world"));
  assert(!("posts" in publicProfile));
  assert(!("postCount" in publicProfile));
  // An independently verified content-service admin is still NOT a body operator.
  await context.addCookies([
    { name: "aiplayer_local_admin", value: publishingCookie, url: origin },
  ]);
  await page.reload();
  await page.getByText(/需要主站已验证的管理员账号/).waitFor();
  assert.equal(
    await page.evaluate(
      async () => (await fetch("/api/body/status?agent=main")).status,
    ),
    403,
  );
  await page.goto(origin + "/body#game-code=" + gameCode);
  await page.getByText(/需要主站已验证的管理员账号/).waitFor();
  assert.equal(new URL(page.url()).hash, "");
  await page.reload();
  await page.getByText(/需要主站已验证的管理员账号/).waitFor();
  assert.equal(
    await page.evaluate(
      () => JSON.parse(sessionStorage.getItem("mineagent.body-pair.main")).code,
    ),
    gameCode,
  );
  await page.goto(origin + "/ai/main");
  await context.addCookies([
    { name: "mineagent_token", value: viewerToken, url: origin },
  ]);
  await page.reload();
  await page
    .getByText("身体详情和控制仅对主站管理员开放。", { exact: true })
    .waitFor();
  assert.equal(await page.locator("#body-stop").count(), 0);
  await context.addCookies([
    { name: "mineagent_token", value: adminToken, url: origin },
  ]);
  await page.reload();
  await page.locator("#body-stop").waitFor();
  assert.equal(await page.locator(".player-body-section > div").getAttribute("aria-live"), null);
  await page.route("**/api/body/status?agent=main", (route) => route.fulfill({ status: 503, json: { error: "service_unavailable" } }));
  await page.reload();
  await page.getByText("暂时无法读取身体服务，请检查连接后重新检查。", { exact: true }).waitFor();
  assert.equal(await page.getByText("登录主站账号", { exact: true }).count(), 0);
  await page.unroute("**/api/body/status?agent=main");
  await page.getByRole("button", { name: "重新检查权限", exact: true }).click();
  await page.locator("#body-stop").waitFor();
  await page.getByText(/private-fixture-world/).waitFor();
  assert.equal(
    await page.evaluate(
      async () => (await fetch("/api/body/status?agent=other")).status,
    ),
    404,
  );
  assert.equal(
    await page.evaluate(
      async () =>
        (
          await fetch("/api/body/control?agent=other", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: '{"action":"stop"}',
          })
        ).status,
    ),
    404,
  );
  await page.getByRole("button", { name: "恢复并继续", exact: true }).click();
  await page.locator("#body-status").filter({ hasText: "在线待机" }).waitFor();
  await page
    .getByLabel("给当前玩家的任务", { exact: true })
    .fill("这是隔离环境的明确任务");
  await page.getByRole("button", { name: "发送任务", exact: true }).click();
  await page
    .locator("#body-replies")
    .getByText("已收到隔离任务。", { exact: true })
    .waitFor({ timeout: 15000 });
  await page.getByRole("button", { name: "停止并暂停", exact: true }).click();
  await page.locator("#body-status").filter({ hasText: "已暂停" }).waitFor();
  assert.equal(await page.locator("#body-pending-count").innerText(), "0");
  // /body remains usable on its own; the fragment is scrubbed and only redeemed
  // after the administrator explicitly confirms the exact game connection.
  await page.goto(origin + "/body#game-code=" + gameCode);
  await page.locator("#body-game-code").waitFor();
  assert.equal(await page.locator("#body-game-code").inputValue(), gameCode);
  assert.equal(new URL(page.url()).hash, "");
  await page.getByRole("button", { name: "核对并授权", exact: true }).click();
  await page
    .getByRole("dialog")
    .getByText(/FixturePlayer/)
    .waitFor();
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "授权此连接", exact: true })
    .click();
  await page
    .locator("#body-game-result")
    .filter({ hasText: "已授权" })
    .waitFor();
  await page
    .getByRole("button", {
      name: "撤销本次网页登录授予的游戏控制",
      exact: true,
    })
    .click();
  await page
    .locator("#body-game-result")
    .filter({ hasText: "已撤销" })
    .waitFor();
  // The unbound player must never inherit the main body's panel.
  await page.goto(origin + "/ai/other");
  await page.getByText("此玩家未接入身体。", { exact: true }).waitFor();
  assert.equal(await page.locator("#body-stop").count(), 0);
  for (const width of [390, 768, 1280]) {
    await page.setViewportSize({ width, height: 900 });
    await page.goto(origin + "/ai/main");
    await page.locator("#body-stop").waitFor();
    if (width === 390) {
      assert.equal(await page.locator(".body-control").evaluate((el) => getComputedStyle(el).position), "static");
      await page.getByLabel("给当前玩家的任务", { exact: true }).focus();
      assert(await page.locator("#body-task-input").evaluate((el) => el.matches(":focus-visible")));
    }
    assert(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
      "overflow " + width,
    );
    await page.evaluate(() => scrollTo(0, 0));
    await page.screenshot({
      path: path.join(artifacts, "profile-" + width + ".png"),
      fullPage: true,
    });
  }
  await page.getByRole("button", { name: "切换深浅主题" }).click();
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.evaluate(() => Promise.all(document.getAnimations().map((a) => a.finished.catch(() => {}))));
  assert(await page.locator("#body-agent-chat").evaluate((el) => getComputedStyle(el).backgroundColor === getComputedStyle(document.querySelector(".body-card")).backgroundColor));
  await page.screenshot({
    path: path.join(artifacts, "profile-dark.png"),
    fullPage: true,
  });
  // Identical polling must retain keyboard focus and avoid repeated live-region
  // replacement. This synthetic approval is UI-only; no approval POST is sent.
  await page.route("**/api/body/approvals?agent=main", (route) => route.fulfill({
    json: { approvals: [{ approvalId: "ui-only", prompt: "仅用于键盘验收的模拟审批" }] },
  }));
  const approve = page.getByRole("button", { name: "批准本次动作", exact: true });
  await approve.waitFor();
  await approve.focus();
  await page.keyboard.press("Tab");
  assert.equal(await page.evaluate(() => document.activeElement.textContent), "拒绝");
  await page.evaluate(() => {
    window.uiApprovalButton = document.activeElement;
    window.uiReplyList = document.querySelector("#body-replies").firstChild;
  });
  const poll = await page.waitForResponse((r) => r.url().includes("/api/body/approvals?agent=main"));
  await poll.finished();
  await page.waitForTimeout(150);
  assert(await page.evaluate(() => document.activeElement === window.uiApprovalButton));
  assert(await page.evaluate(() => document.querySelector("#body-replies").firstChild === window.uiReplyList));
  await page.unroute("**/api/body/approvals?agent=main");
  await page.locator("#body-approval-title").focus();
  await page.getByText("暂无待审核动作。", { exact: true }).waitFor();
  await page.goto(origin + "/ai/new");
  await page.getByRole("heading", { name: "论坛暂未开放" }).waitFor();
  assert.equal(
    await page.evaluate(async () => (await fetch("/api/ai-posts")).status),
    404,
  );
  await page.goto(origin + "/ai/main");
  await page.locator("#body-stop").waitFor();
  await page.evaluate(() => fetch("/_fixture/state?mode=offline"));
  await page.locator("#body-status").filter({ hasText: "玩家离线" }).waitFor();
  await page.evaluate(() => fetch("/_fixture/state?mode=online"));
  await page.locator("#body-status").filter({ hasText: "已暂停" }).waitFor();
  await page.evaluate(() => fetch("/_fixture/state?mode=stale"));
  await page
    .locator("#body-status")
    .filter({ hasText: "状态未知" })
    .waitFor({ timeout: 16000 });
  await page.evaluate(() => fetch("/_fixture/state?mode=online"));
  await page
    .locator("#body-status")
    .filter({ hasText: "已暂停" })
    .waitFor({ timeout: 10000 });
  // Logout/revocation removes the previously rendered private fields as well.
  await context.clearCookies();
  await page
    .getByText(/需要主站已验证的管理员账号/)
    .waitFor({ timeout: 12000 });
  assert.equal(await page.locator("#body-stop").count(), 0);
  assert(
    !(await page
      .locator("main")
      .innerText()
      .then((s) => s.includes("private-fixture-world"))),
  );
  await context.addCookies([
    { name: "mineagent_token", value: adminToken, url: origin },
  ]);
  await page.evaluate(() => fetch("/_fixture/state?mode=content-down"));
  await page.goto(origin + "/body");
  await page.locator("#body-stop").waitFor();
  await page.getByRole("button", { name: "停止并暂停", exact: true }).click();
  await page.locator("#body-status").filter({ hasText: "已暂停" }).waitFor();
  assert.deepEqual(errors, []);
  console.log(
    JSON.stringify({
      result: "passed",
      checks: [
        "Minecraft hierarchy",
        "public/private isolation",
        "main vs independent identity",
        "no body fallback",
        "task/reply on existing Agent",
        "pause/resume",
        "legacy fragment pairing/revoke",
        "forum closed",
        "390/768/1280",
        "dark/reduced motion",
        "shared portal theme / keyboard / stable polling focus",
        "offline/stale/recovery",
        "logout",
        "legacy control survives content-service outage",
      ],
      artifacts,
    }),
  );
} catch (e) {
  console.error("Page errors:", errors);
  throw e;
} finally {
  if (browser) await browser.close();
  fixture.stdin.end();
  await new Promise((r) => {
    if (fixture.exitCode !== null) return r();
    fixture.once("exit", r);
    setTimeout(() => {
      fixture.kill("SIGTERM");
      r();
    }, 4000).unref();
  });
}
