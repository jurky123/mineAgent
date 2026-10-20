// Full body E2E against an explicitly marked disposable Paper world and local mock model.
// Starts/stops only its own test Paper/backend/body processes. Never reads production config.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import os from "node:os";
import { spawn } from "node:child_process";
import http from "node:http";
import net from "node:net";
import { randomBytes, randomUUID, createHash } from "node:crypto";
import { pathToFileURL } from "node:url";
import { chromium } from "playwright";
const paperDir = process.argv[2],
  binary = process.argv[3] || "/tmp/mineagent-body-v1";
if (!paperDir)
  throw Error(
    "Usage: node scripts/body-e2e.mjs <marked disposable paper directory> [test binary]",
  );
assert.ok(
  (
    await fs.readFile(path.join(paperDir, ".mineagent-body-test"), "utf8")
  ).includes("isolated test"),
);
const repo = process.cwd(),
  temp = await fs.mkdtemp(path.join(os.tmpdir(), "mineagent-body-e2e-"));
await fs.chmod(temp, 0o700);
const env = { PATH: process.env.PATH, HOME: process.env.HOME, LANG: "C.UTF-8" };
const wait = (ms) => new Promise((r) => setTimeout(r, ms));
async function until(fn, description, ms = 20000) {
  const start = Date.now();
  let last;
  while (Date.now() - start < ms) {
    try {
      const v = await fn();
      if (v) return v;
    } catch (e) {
      last = e;
    }
    await wait(100);
  }
  throw Error(description + (last ? ": " + last.message : ""));
}
async function port() {
  const s = net.createServer();
  await new Promise((r) => s.listen(0, "127.0.0.1", r));
  const p = s.address().port;
  await new Promise((r) => s.close(r));
  return p;
}
const webPort = await port(),
  wsPort = await port(),
  origin = `http://localhost:${webPort}`;
const bodyToken = randomBytes(24).toString("hex"),
  paperToken = randomBytes(24).toString("hex"),
  profileToken = randomBytes(24).toString("hex");
const logs = { paper: "", backend: "", body: "" },
  children = [];
let browser, paper, backend, body;
const scenarios = new Map();
let calls = 0;
const model = http.createServer(async (req, res) => {
  let raw = "";
  for await (const c of req) raw += c;
  let request;
  try {
    request = JSON.parse(raw);
  } catch {
    res.writeHead(400).end();
    return;
  }
  const tool = request.messages?.findLast((m) => m.role === "tool");
  let message,
    finish = "stop";
  if (tool) {
    const scenario = scenarios.get(tool.tool_call_id);
    if (scenario) {
      try {
        scenario.result = JSON.parse(tool.content);
      } catch {
        scenario.result = tool.content;
      }
    }
    message = { role: "assistant", content: "fixture finished" };
  } else {
    const scenario = [...scenarios.values()].find(
      (s) => raw.includes(s.query) && !s.issued,
    );
    if (!scenario) {
      message = { role: "assistant", content: "fixture idle" };
    } else {
      scenario.issued = true;
      calls++;
      message = {
        role: "assistant",
        content: "",
        tool_calls: [
          {
            id: scenario.id,
            type: "function",
            function: {
              name: scenario.tool,
              arguments: JSON.stringify(scenario.args),
            },
          },
        ],
      };
      finish = "tool_calls";
    }
  }
  res.writeHead(200, { "Content-Type": "application/json" });
  res.end(
    JSON.stringify({
      id: randomUUID(),
      object: "chat.completion",
      model: "body-fixture",
      choices: [{ index: 0, message, finish_reason: finish }],
      usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 },
    }),
  );
});
await new Promise((r) => model.listen(0, "127.0.0.1", r));
function start(kind, cmd, args, cwd, extra = {}) {
  const child = spawn(cmd, args, {
    cwd,
    env: { ...env, ...extra },
    stdio: ["pipe", "pipe", "pipe"],
  });
  children.push(child);
  for (const stream of [child.stdout, child.stderr])
    stream.on("data", (b) => {
      logs[kind] = (logs[kind] + b.toString()).slice(-150000);
    });
  child.on("error", (e) => {
    logs[kind] += "\n" + e.message;
  });
  return child;
}
let token = "";
async function api(route, method = "GET", data, asToken = token) {
  const res = await fetch(origin + route, {
    method,
    headers: {
      Origin: origin,
      ...(asToken ? { Authorization: "Bearer " + asToken } : {}),
      ...(data !== undefined ? { "Content-Type": "application/json" } : {}),
    },
    ...(data !== undefined ? { body: JSON.stringify(data) } : {}),
  });
  let json;
  try {
    json = await res.json();
  } catch {
    json = {};
  }
  return { ...json, httpStatus: res.status };
}
const state = () => api("/api/body/status");
const control = (action) => api("/api/body/control", "POST", { action });
const command = async (text) => {
  paper.stdin.write(text + "\n");
  await wait(200);
};
async function send(tool, args, awaitResult = true) {
  console.log("fixture tool: " + tool);
  const id = "body-" + randomUUID();
  const scenario = { id, query: "fixture-case-" + id, tool, args };
  scenarios.set(id, scenario);
  const sent = await api("/api/send", "POST", { text: scenario.query });
  assert.equal(sent.httpStatus, 200, JSON.stringify(sent));
  scenario.conv = sent.conv;
  if (awaitResult)
    await until(() => scenario.result, "tool result " + tool, 30000);
  return scenario;
}
function confirmed(s) {
  assert.equal(s.result.ok, true, JSON.stringify(s.result));
  assert.equal(s.result.outcome, "confirmed", JSON.stringify(s.result));
}
async function pending() {
  return until(async () => {
    const r = await api("/api/body/approvals");
    return r.approvals?.[0];
  }, "portal approval");
}
try {
  await fs.mkdir(path.join(paperDir, "plugins", "MineAgent"), {
    recursive: true,
  });
  const jars = await fs.readdir(path.join(repo, "paper-plugin/build/libs"));
  const jar = jars.find((x) => x.endsWith(".jar"));
  assert.ok(jar, "build the Paper plugin first");
  await fs.copyFile(
    path.join(repo, "paper-plugin/build/libs", jar),
    path.join(paperDir, "plugins", jar),
  );
  await fs.writeFile(
    path.join(paperDir, "plugins/MineAgent/config.yml"),
    `backend:\n  url: "ws://127.0.0.1:${wsPort}/ws"\n  token: "${paperToken}"\n  reconnect:\n    initial-delay-ms: 1000\n    max-delay-ms: 10000\n  heartbeat-ms: 10000\n`,
    { mode: 0o600 },
  );
  const cfg = {
    listen: `127.0.0.1:${wsPort}`,
    allowedRoles: ["minecraft", "minecraft-body"],
    storage: { path: path.join(temp, "test.db") },
    workspace: { root: path.join(temp, "workspace") },
    model: {
      baseURL: `http://127.0.0.1:${model.address().port}`,
      name: "body-fixture",
      apiKey: "fixture",
    },
    agent: { progressSummary: false },
    web: {
      listen: `127.0.0.1:${webPort}`,
      dataDir: path.join(temp, "web"),
      adminUsers: ["BodyAdmin"],
      passkeyRPID: "localhost",
      passkeyOrigins: [origin],
      minIntervalMs: 1,
    },
    body: {
      enabled: true,
      mode: "assist",
      id: "main",
      name: "MineBodyV1",
      expectedMcVersion: "26.2",
      worldMappings: [
        { worldId: "m0-world", dimension: "minecraft:overworld" },
      ],
      zones: {
        mode: "allowlist",
        areas: [
          {
            name: "test",
            worldId: "m0-world",
            min: [-32, -64, -32],
            max: [64, 100, 64],
          },
        ],
      },
      limits: {
        actionsPerMin: 120,
        blocksPerMin: 120,
        chatPerMin: 20,
        commandsPerMin: 10,
        approvalsPerMin: 20,
      },
      profile: {
        enabled: true,
        tokenHash: createHash("sha256").update(profileToken).digest("hex"),
      },
    },
  };
  await fs.writeFile(path.join(temp, "config.json"), JSON.stringify(cfg), {
    mode: 0o600,
  });
  backend = start(
    "backend",
    binary,
    ["--config", path.join(temp, "config.json")],
    repo,
    { MINEAGENT_BODY_TOKEN: bodyToken, MINEAGENT_TOKEN: paperToken },
  );
  await until(async () => {
    try {
      return (await fetch(origin + "/api/version")).ok;
    } catch {
      return false;
    }
  }, "test backend startup");
  paper = start(
    "paper",
    "nice",
    [
      "-n",
      "10",
      "java",
      "-Xms128M",
      "-Xmx768M",
      "-XX:ActiveProcessorCount=1",
      "-jar",
      "paper-26.2-124.jar",
      "--nogui",
    ],
    paperDir,
  );
  await until(
    () => logs.paper.includes('For help, type "help"'),
    "test Paper startup",
    60000,
  );
  const login = await api(
    "/api/auth/password/setup",
    "POST",
    {
      name: "BodyAdmin",
      password: "body fixture password!",
      confirm: "body fixture password!",
    },
    "",
  );
  assert.equal(login.httpStatus, 200, JSON.stringify(login));
  token = login.token;
  const nodeConfig = {
    backend: { url: `ws://127.0.0.1:${wsPort}/ws`, bodyId: "main" },
    minecraft: {
      host: "127.0.0.1",
      port: 25666,
      version: "26.1",
      targetVersion: "26.2",
      username: "MineBodyV1",
      auth: "offline",
      viaProxy: true,
    },
  };
  await fs.writeFile(path.join(temp, "body.json"), JSON.stringify(nodeConfig), {
    mode: 0o600,
  });
  // Test-only path diagnostics; production execution is unchanged.
  const instrument = path.join(temp, "path-diagnostics.mjs");
  await fs.writeFile(
    instrument,
    `
import { MinecraftBody } from ${JSON.stringify(pathToFileURL(path.join(repo, "body/src/minecraft.js")).href)};
const original= MinecraftBody.prototype.movements;
MinecraftBody.prototype.movements=function(){
 original.call(this);
 if(this.bot._fixtureDiagnostics)return;
 this.bot._fixtureDiagnostics=true;const recent=[];
 this.bot.on('path_reset',reason=>{recent.push({reset:reason});if(recent.length>8)recent.shift();});
 this.bot.on('path_update',p=>{recent.push({status:p.status,path:p.path.slice(0,5).map(n=>[n.x,n.y,n.z]),pos:this.bot.entity.position});if(recent.length>8)recent.shift();if(p.status==='noPath'||p.status==='timeout')console.log(JSON.stringify({fixturePath:recent}));});
};
`,
    { mode: 0o600 },
  );
  body = start(
    "body",
    "node",
    [
      "--import",
      instrument,
      "body/src/index.js",
      "--config",
      path.join(temp, "body.json"),
    ],
    repo,
    { MINEAGENT_BODY_TOKEN: bodyToken },
  );
  await until(
    async () => {
      const st = await state();
      return st.state?.connected && st.ready;
    },
    "body handshake/spawn",
    30000,
  );
  await command("difficulty peaceful");
  await command("tp MineBodyV1 0 -60 0");
  await command("clear MineBodyV1");
  await command(
    "item replace entity MineBodyV1 weapon.mainhand with minecraft:diamond_axe",
  );
  await command("give MineBodyV1 minecraft:oak_planks 16");
  await command("give MineBodyV1 minecraft:golden_apple 8");
  await command("give MineBodyV1 minecraft:tnt 8");
  await command("fill -10 -61 -10 32 -61 16 minecraft:grass_block");
  await command("fill -10 -60 -10 32 -57 16 minecraft:air");
  await command("setblock 2 -60 0 minecraft:oak_log");
  await command("setblock 1 -60 1 minecraft:air");
  await command("setblock 2 -60 1 minecraft:air");
  await command("setblock 2 -60 2 minecraft:air");
  await command("setblock 3 -60 0 minecraft:oak_door[half=lower,facing=north]");
  await command("setblock 3 -59 0 minecraft:oak_door[half=upper,facing=north]");
  await wait(2200);
  assert.equal((await control("resume")).httpStatus, 200);
  confirmed(await send("body_observe", {}));
  confirmed(await send("body_inventory", {}));
  confirmed(await send("body_find_blocks", { blocks: "oak_log", radius: 16 }));
  confirmed(await send("body_goto", { x: 9, y: -60, z: 0, range: 1 }));
  confirmed(await send("body_goto", { x: 0, y: -60, z: 0, range: 1 }));
  confirmed(await send("body_look_at", { x: 2, y: -60, z: 0 }));
  confirmed(await send("body_dig", { x: 2, y: -60, z: 0 }));
  confirmed(
    await send("body_place", { x: 1, y: -60, z: 1, item: "oak_planks" }),
  );
  confirmed(await send("body_interact", { x: 3, y: -60, z: 0 }));
  confirmed(await send("body_use_item", { item: "golden_apple" }));
  assert.equal(
    (await send("body_say", { text: "isolated body end-to-end check" })).result
      .outcome,
    "submitted",
  );
  assert.equal(
    (await send("body_command", { command: "help" })).result.outcome,
    "submitted",
  );
  const high = await send(
    "body_place",
    {
      x: 2,
      y: -60,
      z: 1,
      item: "tnt",
      reason: "隔离测试场放置一枚不点燃的 TNT",
    },
    false,
  );
  const approval = await pending();
  assert.equal(high.result, undefined);
  assert.equal(
    (
      await api("/api/body/approvals/" + approval.approvalId, "POST", {
        approved: true,
      })
    ).httpStatus,
    200,
  );
  await until(() => high.result, "approval resume");
  confirmed(high);
  const moving = await send(
    "body_goto",
    { x: 25, y: -60, z: 10, range: 1, timeoutSec: 20 },
    false,
  );
  await until(async () => {
    if (moving.result) throw Error(JSON.stringify(moving.result));
    const st = await state();
    return st.state?.task?.tool === "body_goto" && st.state.pos[0] > 3;
  }, "moving body");
  const before = Date.now();
  const stopped = await control("stop");
  assert.equal(stopped.httpStatus, 200, JSON.stringify(stopped));
  assert.ok(Date.now() - before < 2000, "stop should acknowledge within 2s");
  await until(() => moving.result, "cancelled movement result");
  assert.equal(moving.result.ok, false);
  assert.equal((await state()).paused, true);
  const profile = await api(
    "/api/body/profile",
    "GET",
    undefined,
    profileToken,
  );
  assert.equal(profile.httpStatus, 200);
  assert.equal(profile.profileVersion, 1);
  assert.equal(profile.position, undefined);
  assert.equal(
    (await api("/api/body/control", "POST", { action: "resume" }, profileToken))
      .httpStatus,
    403,
  );
  assert.equal((await control("resume")).httpStatus, 200);
  await command("tp MineBodyV1 0 -60 0");
  await wait(400);
  assert.equal((await control("resume")).httpStatus, 200);
  const stale = await send(
    "body_place",
    { x: 2, y: -60, z: 2, item: "tnt", reason: "验证审批后目标变化不会执行" },
    false,
  );
  const pendingStale = await pending();
  await command("setblock 2 -60 2 minecraft:stone");
  assert.equal(
    (
      await api("/api/body/approvals/" + pendingStale.approvalId, "POST", {
        approved: true,
      })
    ).httpStatus,
    200,
  );
  await until(() => stale.result, "stale action rejection");
  assert.equal(stale.result.ok, false);
  assert.equal(stale.result.outcome, "not_executed");
  // Real browser validates the control page, mobile overflow and authenticated SSE cookie path.
  browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({
    viewport: { width: 390, height: 844 },
  });
  const page = await context.newPage();
  const pageErrors = [];
  page.on("pageerror", (e) => pageErrors.push(e.message));
  await context.addCookies([
    { name: "mineagent_token", value: token, url: origin },
  ]);
  await page.goto(origin + "/body");
  await page.evaluate((t) => localStorage.setItem("mineagent.token", t), token);
  await page.reload();
  await page.locator("#body-name").filter({ hasText: "MineBodyV1" }).waitFor();
  assert.ok(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  );
  await page.screenshot({
    path: path.join(temp, "body-mobile.png"),
    fullPage: true,
  });
  await page.locator("#body-stop").click();
  await until(async () => (await state()).paused, "browser stop");
  assert.deepEqual(pageErrors, []);
  await browser.close();
  browser = null;
  confirmed(await send("body_stop", {}));
  console.log(
    JSON.stringify({
      passed: true,
      tools: 12,
      modelCalls: calls,
      checks: [
        "real Paper world identity",
        "per-user authenticated tool context",
        "all core actions",
        "portal approval and resume",
        "stale target rejection",
        "stop during movement",
        "profile scope/privacy",
        "mobile control UI and SSE",
      ],
      artifacts: temp,
    }),
  );
} catch (e) {
  // Fixture-generated credentials are redacted before showing any test diagnostics.
  const redact = (s) =>
    s
      .replaceAll(bodyToken, "<body-token>")
      .replaceAll(paperToken, "<paper-token>")
      .replaceAll(profileToken, "<profile-token>")
      .replaceAll(token || "__never__", "<admin-token>");
  for (const [kind, text] of Object.entries(logs))
    console.error(kind + ":\n" + redact(text.slice(-6000)));
  throw e;
} finally {
  if (browser) await browser.close();
  if (body && body.exitCode === null) body.kill("SIGTERM");
  if (paper && paper.exitCode === null) paper.stdin.end("stop\n");
  await Promise.all(
    children
      .filter((c) => c !== backend)
      .map(
        (c) =>
          new Promise((resolve) => {
            if (c.exitCode !== null) return resolve();
            c.once("exit", resolve);
            setTimeout(() => {
              if (c.exitCode === null) c.kill("SIGTERM");
            }, 10000).unref();
          }),
      ),
  );
  if (backend && backend.exitCode === null) backend.kill("SIGTERM");
  await new Promise((r) => model.close(r));
}
