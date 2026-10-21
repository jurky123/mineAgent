// Full body E2E against an explicitly marked disposable Paper world and local mock model.
// Starts/stops only disposable processes. Real-model UX is separately opt-in; consumes only the configured model connection, never logs credentials.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import os from "node:os";
import { spawn } from "node:child_process";
import http from "node:http";
import net from "node:net";
import { randomBytes, randomUUID, createHash } from "node:crypto";
import { pathToFileURL } from "node:url";
import { createRequire } from "node:module";
import { chromium } from "playwright";
import { bodyTestResourcePreflight } from "./body-test-resources.mjs";
await bodyTestResourcePreflight();
const paperDir = process.argv[2],
  binary = process.argv[3] || "/tmp/mineagent-body-v1";
const proxyJar = process.argv[4];
const worldEditJar = process.env.MINEAGENT_WORLDEDIT_TEST_JAR;
const uxModel = process.env.MINEAGENT_UX_MODEL_CONFIG
  ? JSON.parse(await fs.readFile(process.env.MINEAGENT_UX_MODEL_CONFIG, "utf8"))
      .model
  : null;
const uxModelMetrics = [];
const uxRunID = randomUUID();
const uxOnly = !!uxModel && process.env.MINEAGENT_UX_ONLY === "1";
const require = createRequire(new URL("../body/package.json", import.meta.url));
const mineflayer = require("mineflayer"),
  nbt = require("prismarine-nbt");
const { Vec3 } = require("vec3");
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
const properties = await fs.readFile(
  path.join(paperDir, "server.properties"),
  "utf8",
);
for (const entry of [
  "server-ip=127.0.0.1",
  "server-port=25665",
  "level-name=m0-world",
])
  assert.ok(
    properties.split(/\r?\n/).includes(entry),
    "isolated Paper must use " + entry,
  );
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
const logs = { paper: "", backend: "", body: "", proxy: "", portal: "" },
  children = [];
let browser, paper, backend, body, proxy;
const bots = [];
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
  const lastUser =
    request.messages?.findLastIndex((m) => m.role === "user") ?? -1;
  const tool = request.messages
    ?.slice(lastUser + 1)
    .findLast((m) => m.role === "tool");
  if (uxModel && request.messages?.[lastUser]?.content?.includes("[UX REAL]")) {
    const start = Date.now();
    try {
      const upstream = await fetch(
        uxModel.baseURL.replace(/\/$/, "") + "/chat/completions",
        {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            Authorization: "Bearer " + uxModel.apiKey,
            "User-Agent": req.headers["user-agent"] || "MineAgent/ux",
            "x-opencode-session":
              String(req.headers["x-opencode-session"] || "mineagent") +
              "-ux-" +
              uxRunID,
          },
          body: JSON.stringify({ ...request, model: uxModel.name }),
          signal: AbortSignal.timeout(60000),
        },
      );
      if (!upstream.ok) {
        const errorText = await upstream.text();
        let safe = JSON.stringify({ request, error: errorText });
        for (const key of [
          uxModel.apiKey,
          bodyToken,
          paperToken,
          profileToken,
          token,
        ])
          if (key) safe = safe.replaceAll(key, "<redacted>");
        await fs.writeFile(path.join(temp, "ux-upstream-failure.json"), safe, {
          mode: 0o600,
        });
      }
      const data = upstream.ok
        ? await upstream.text()
        : JSON.stringify({
            error: {
              message: "isolated UX model returned HTTP " + upstream.status,
            },
          });
      uxModelMetrics.push({
        elapsedMs: Date.now() - start,
        status: upstream.status,
        retryAfter: upstream.headers.get("retry-after"),
        effort: request.reasoning_effort,
        tools: request.messages
          .filter((m) => m.role === "assistant")
          .flatMap((m) => (m.tool_calls || []).map((t) => t.function.name)),
      });
      await fs.writeFile(
        path.join(temp, "ux-model-metrics.json"),
        JSON.stringify(uxModelMetrics, null, 2),
        { mode: 0o600 },
      );
      res.writeHead(upstream.status, {
        "Content-Type":
          upstream.headers.get("content-type") || "application/json",
      });
      res.end(data);
    } catch {
      res.writeHead(502).end(
        JSON.stringify({
          error: { message: "isolated UX upstream unavailable" },
        }),
      );
    }
    return;
  }
  let message,
    finish = "stop";
  let nextScenario;
  if (tool) {
    const scenario = scenarios.get(tool.tool_call_id);
    if (scenario) {
      scenario.modelRequests = (scenario.modelRequests || 0) + 1;
      let result;
      try {
        result = JSON.parse(tool.content);
      } catch {
        result = tool.content;
      }
      scenario.results ??= [];
      scenario.results.push(result);
      if (result.ok && scenario.results.length < (scenario.repeat || 1)) {
        nextScenario = scenario;
      } else scenario.result = result;
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
      scenario.modelRequests = 1;
      await scenario.beforeIssue?.();
      nextScenario = scenario;
    }
  }
  if (nextScenario) {
    const scenario = nextScenario;
    const callId = scenario.id + "-" + (scenario.results?.length || 0);
    scenarios.set(callId, scenario);
    calls++;
    message = {
      role: "assistant",
      content: "",
      tool_calls: [
        {
          id: callId,
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
async function send(tool, args, awaitResult = true, options = {}) {
  console.log("fixture tool: " + tool);
  const id = "body-" + randomUUID();
  const scenario = { id, query: "fixture-case-" + id, tool, args, ...options };
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
async function connectController() {
  const bot = mineflayer.createBot({
    host: "127.0.0.1",
    port: 25666,
    username: "BodyController",
    auth: "offline",
    version: "26.1",
  });
  bots.push(bot);
  bot.dialogs = [];
  bot.on("error", () => {});
  bot._client.on("show_dialog", (p) =>
    bot.dialogs.push(nbt.simplify(p.dialog.data)),
  );
  await until(() => bot.entity, "controller spawn", 30000);
  await wait(800);
  return bot;
}
function vi(n) {
  const out = [];
  do {
    let b = n & 127;
    n >>>= 7;
    if (n) b |= 128;
    out.push(b);
  } while (n);
  return Buffer.from(out);
}
function click(bot, dialog, label, text) {
  const button = [...(dialog.actions || []), dialog.exit_action]
    .filter(Boolean)
    .find((b) => typeof b.label === "string" && b.label.includes(label));
  assert.ok(button, "missing native button: " + label);
  const action = button.action,
    payload = { id: nbt.intArray(action.additions.id) };
  if (text !== undefined) payload.question = nbt.string(text);
  const named = nbt.writeUncompressed(nbt.comp(payload)),
    tag = Buffer.concat([named.subarray(0, 1), named.subarray(3)]),
    id = Buffer.from(action.id);
  const mappings =
    require("minecraft-data")("26.1").protocol.play.toServer.types.packet[1][0]
      .type[1].mappings;
  const packet = Number(
    Object.entries(mappings).find(([, n]) => n === "custom_click_action")[0],
  );
  bot._client.writeRaw(
    Buffer.concat([vi(packet), vi(id.length), id, vi(tag.length), tag]),
  );
}
async function openPanel(bot) {
  const n = bot.dialogs.length;
  bot.chat("/agent");
  return until(() => bot.dialogs[n], "native home");
}
async function navigate(bot, dialog, label, text) {
  const n = bot.dialogs.length;
  click(bot, dialog, label, text);
  return until(() => bot.dialogs[n], "native click " + label);
}
async function bodyTask(bot, dialog, tool, args) {
  const id = "body-ui-" + randomUUID(),
    scenario = { id, query: "fixture-case-" + id, tool, args };
  scenarios.set(id, scenario);
  const next = await navigate(bot, dialog, "发送任务", scenario.query);
  return { scenario, dialog: next };
}
try {
  await fs.mkdir(path.join(paperDir, "plugins", "MineAgent"), {
    recursive: true,
  });
  const version = (
    await fs.readFile(path.join(repo, "paper-plugin/gradle.properties"), "utf8")
  ).match(/^version=(.+)$/m)[1];
  const jar = `MineAgent-${version}.jar`;
  for (const file of await fs.readdir(path.join(paperDir, "plugins")))
    if (/^MineAgent.*\.jar$/.test(file))
      await fs.unlink(path.join(paperDir, "plugins", file));
  await fs.copyFile(
    path.join(repo, "paper-plugin/build/libs", jar),
    path.join(paperDir, "plugins", jar),
  );
  if (worldEditJar) {
    await fs.copyFile(
      worldEditJar,
      path.join(paperDir, "plugins/WorldEdit.jar"),
    );
    await fs.writeFile(
      path.join(paperDir, "permissions.yml"),
      `mineagent-worldedit-fixture:
  default: true
  children:
    worldedit.selection.pos: true
    worldedit.region.set: true
    worldedit.region.move: true
    worldedit.clipboard.copy: true
    worldedit.clipboard.paste: true
    worldedit.history.undo: true
    worldedit.history.undo.self: true
    worldedit.history.redo: true
    worldedit.history.redo.self: true
`,
    );
  }
  await fs.writeFile(
    path.join(paperDir, "plugins/MineAgent/config.yml"),
    `backend:\n  url: "ws://127.0.0.1:${wsPort}/ws"\n  token: "${paperToken}"\n  reconnect:\n    initial-delay-ms: 1000\n    max-delay-ms: 10000\n  heartbeat-ms: 10000\n`,
    { mode: 0o600 },
  );
  if (worldEditJar)
    await fs.appendFile(
      path.join(paperDir, "plugins/MineAgent/config.yml"),
      `worldedit-protection:
  enabled: true
  player: MineBodyV1
  world: m0-world
  radius: 512
`,
    );
  let aiProxy;
  if (uxModel) {
    assert.ok(
      process.env.MINEAGENT_UX_AIPLAYER_BINARY,
      "UX requires a temporary AI portal binary",
    );
    const aiPort = await port();
    aiProxy = `http://127.0.0.1:${aiPort}`;
    const profileFile = path.join(temp, "profile-token");
    await fs.writeFile(profileFile, profileToken, { mode: 0o600 });
    const portalConfig = path.join(temp, "aiplayer.json");
    await fs.writeFile(
      portalConfig,
      JSON.stringify({
        listen: `127.0.0.1:${aiPort}`,
        origin,
        database: path.join(temp, "aiplayer.db"),
        agents: [{ id: "main", name: "MineBodyV1", displayName: "MineBodyV1" }],
        profile: {
          agentId: "main",
          url: origin + "/api/body/profile",
          tokenFile: profileFile,
        },
      }),
      { mode: 0o600 },
    );
    start(
      "portal",
      process.env.MINEAGENT_UX_AIPLAYER_BINARY,
      ["--config", portalConfig],
      repo,
    );
    await until(
      async () => (await fetch(aiProxy + "/ai/main")).ok,
      "isolated AI portal",
    );
  }
  if (process.env.MINEAGENT_ESSENTIALS_TEST_JAR) {
    await fs.copyFile(
      process.env.MINEAGENT_ESSENTIALS_TEST_JAR,
      path.join(paperDir, "plugins/EssentialsX.jar"),
    );
    await fs.appendFile(
      path.join(paperDir, "permissions.yml"),
      "    essentials.help: true\n    essentials.list: true\n",
    );
  }
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
    agent: {
      progressSummary: false,
      summaryMessages: 1000,
      summaryTokens: 1000000,
    },
    web: {
      listen: `127.0.0.1:${webPort}`,
      dataDir: path.join(temp, "web"),
      adminUsers: ["BodyAdmin"],
      passkeyRPID: "localhost",
      passkeyOrigins: [origin],
      minIntervalMs: 1,
      aiPlayerProxy: aiProxy,
    },
    body: {
      enabled: true,
      mode: "assist",
      id: "main",
      name: "MineBodyV1",
      expectedMcVersion: "26.2",
      maxSteps: uxModel ? 12 : 0, // explicit unlimited soak only in deterministic regression
      maxConsecutiveReads: uxModel ? 3 : 64,
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
        commandsPerMin: 120,
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
  if (proxyJar) {
    proxy = start(
      "proxy",
      "nice",
      [
        "-n",
        "10",
        "java",
        "-Xms64M",
        "-Xmx320M",
        "-XX:ActiveProcessorCount=1",
        "-jar",
        path.resolve(proxyJar),
        "cli",
        "--bind-address",
        "127.0.0.1:25666",
        "--target-address",
        "127.0.0.1:25665",
        "--target-version",
        "26.2",
        "--log-ips",
        "false",
        "--chat-signing",
        "false",
      ],
      path.dirname(path.resolve(proxyJar)),
    );
    await until(
      () => logs.proxy.includes("25666"),
      "test proxy startup",
      30000,
    );
  }
  paper = start(
    "paper",
    "nice",
    [
      "-n",
      "10",
      "java",
      "-Xms128M",
      "-Xmx512M",
      "-XX:MaxDirectMemorySize=64M",
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
    150000,
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
MinecraftBody.prototype.movements=function(...args){
 original.apply(this,args);
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
    90000,
  );
  await command("difficulty peaceful");
  if (worldEditJar) await command("setworldspawn 1024 -60 0");
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
  if (!uxOnly) {
    const health = await send("body_status", {});
    confirmed(health);
    assert.equal(health.result.data.controlConnected, true);
    if (!uxModel) {
      const longRead = await send("body_observe", {}, true, {
        repeat: 40,
        beforeIssue: async () =>
          assert.equal((await control("resume")).httpStatus, 200),
      });
      confirmed(longRead);
      assert.equal(
        longRead.results.length,
        40,
        "same task continues beyond former 12/32 round limits after initial resume",
      );
    }
    // Build allowlists do not fence a player's walking position or path.
    await command("tp MineBodyV1 65 -60 0");
    await wait(1500);
    assert.equal((await control("resume")).httpStatus, 200);
    confirmed(await send("body_goto", { x: 68, y: -60, z: 0, range: 1 }));
    assert.ok(
      (await state()).state.pos[0] > 66,
      "actual movement outside the edit allowlist",
    );
    await command("tp MineBodyV1 0 -60 0");
    await wait(1500);
    assert.equal((await control("resume")).httpStatus, 200);
    const terrainObservation = await send("body_observe", {});
    confirmed(terrainObservation);
    assert.equal(terrainObservation.result.data.terrain.columns.length, 49);
    assert.equal(
      terrainObservation.result.data.terrain.standablePoints.length,
      3,
    );
    const repeatedObservation = await send("body_observe", {}, true, {
      repeat: 2,
    });
    confirmed(repeatedObservation);
    assert.equal(repeatedObservation.results[1].data.unchanged, true);
    confirmed(await send("body_inventory", {}));
    confirmed(
      await send("body_find_blocks", { blocks: "oak_log", radius: 16 }),
    );
    const badHeight = await send("body_goto", { x: 9, y: -55, z: 0, range: 1 });
    assert.equal(badHeight.result.code, "target_above_ground");
    assert.equal(badHeight.result.data.suggestedY, -60);
    confirmed(await send("body_goto", { x: 9, z: 0, range: 1 }));
    confirmed(await send("body_goto", { x: 0, y: -60, z: 0, range: 1 }));
    // Explicit surface swimming: no implicit dive for ordinary ground goals.
    // Restore the fixture afterwards: flowing water must not
    // change the terrain used by later ground-only stop/collision tests.
    await command("fill 3 -60 -1 26 -59 22 air replace water");
    await command("fill 9 -60 5 19 -59 15 air");
    await command("fill 10 -64 6 18 -64 14 stone");
    await command("fill 10 -63 6 18 -60 14 water");
    await wait(500);
    const dryOnly = await send("body_goto", { x: 14, z: 10, range: 1 });
    assert.equal(dryOnly.result.code, "water_only");
    const swim = await send("body_goto", {
      x: 14,
      y: -60,
      z: 10,
      range: 1,
      movement: "swim",
      timeoutSec: 20,
    });
    confirmed(swim);
    assert.ok(
      (await state()).state.pos[0] > 12,
      "real body reaches pool surface",
    );
    // Action cleanup deliberately stops all controls. A later swim must recover
    // from real idle sinking, rather than assuming that feet stayed at surface Y.
    await wait(6500);
    assert.ok((await state()).state.pos[1] < -61, "idle body really sinks");
    const shore = await send("body_goto", {
      x: 0,
      y: -60,
      z: 0,
      range: 1,
      movement: "swim",
      timeoutSec: 20,
    });
    confirmed(shore);
    assert.ok(shore.result.data.surfaceRecovery.fromY < -61);
    // Exact production shape: a waterlogged half-step leading to a dry dock.
    // Several millimetre corrections must not defeat the normal step/jump.
    await command("fill 13 -61 8 15 -61 8 oak_planks");
    await command(
      "fill 13 -60 9 15 -60 9 oak_slab[type=bottom,waterlogged=true]",
    );
    await command("fill 13 -60 10 15 -60 10 oak_planks");
    await command("fill 13 -59 11 15 -59 11 oak_slab[type=bottom]");
    await command("fill 13 -59 12 15 -59 12 oak_planks");
    await command("fill 12 -59 13 16 -59 15 oak_planks");
    await wait(500);
    confirmed(
      await send("body_goto", {
        x: 14.5,
        y: -60,
        z: 8.5,
        movement: "swim",
        range: 1,
        timeoutSec: 20,
      }),
    );
    confirmed(
      await send("body_goto", {
        x: 14.5,
        y: -58,
        z: 14.5,
        movement: "swim",
        range: 1,
        timeoutSec: 20,
      }),
    );
    assert.ok(
      (await state()).state.pos[1] > -58.1,
      "real body stands on dock after wet half-steps",
    );
    confirmed(
      await send("body_goto", {
        x: 0,
        y: -60,
        z: 0,
        movement: "swim",
        range: 1,
        timeoutSec: 20,
      }),
    );
    await command("fill 9 -61 5 19 -61 15 grass_block");
    await command("fill 10 -60 6 18 -57 15 air");
    await command("fill 3 -60 -1 26 -59 22 air replace water");
    console.log(
      "Navigation passed: full arrival region, explicit surface swimming, ground water refusal and return to shore",
    );

    confirmed(await send("body_look_at", { x: 2, y: -60, z: 0 }));
    confirmed(await send("body_dig", { x: 2, y: -60, z: 0 }));
    confirmed(
      await send("body_place", { x: 1, y: -60, z: 1, item: "oak_planks" }),
    );
    confirmed(await send("body_interact", { x: 3, y: -60, z: 0 }));
    confirmed(await send("body_use_item", { item: "golden_apple" }));
    assert.equal(
      (await send("body_say", { text: "isolated body end-to-end check" }))
        .result.outcome,
      "submitted",
    );
    assert.equal(
      (await send("body_command", { command: "help" })).result.outcome,
      "submitted",
    );
    if (worldEditJar) {
      console.log(
        "WorldEdit protection: real agent -> command -> guarded extent",
      );
      await command("setworldspawn 0 -60 0");
      await command("forceload add 512 0 528 0");
      await command("tp MineBodyV1 0 -60 0");
      await wait(1500);
      assert.equal((await control("resume")).httpStatus, 200);
      await command("setblock 512 -60 0 minecraft:glass");
      await command("setblock 513 -60 0 minecraft:air");
      confirmed(await send("body_goto", { x: 2, y: -60, z: 0, range: 1 }));
      const blockedDig = await send("body_dig", { x: 0, y: -61, z: 0 });
      assert.equal(blockedDig.result.code, "spawn_protected");
      const blockedPlace = await send("body_place", {
        x: 1,
        y: -60,
        z: 2,
        item: "oak_planks",
      });
      assert.equal(blockedPlace.result.code, "spawn_protected");
      const we = async (text) => {
        const result = (await send("body_command", { command: text })).result;
        assert.equal(result.ok, true, JSON.stringify(result));
        assert.equal(result.outcome, "submitted");
        return result;
      };
      const blockIs = async (x, block) => {
        const marker = "worldedit-check-" + randomUUID();
        await command(
          `execute if block ${x} -60 0 minecraft:${block} run say ${marker}`,
        );
        await until(
          () => logs.paper.includes("[Server] " + marker),
          "actual WorldEdit block " + x + " " + block,
        );
      };
      await we("//pos1 513,-60,0");
      await we("//pos2 513,-60,0");
      await we("//set stone");
      await blockIs(513, "stone");
      await we("//undo");
      await blockIs(513, "air");
      await we("//redo");
      await blockIs(513, "stone");
      await we("//pos1 512,-60,0");
      await we("//pos2 512,-60,0");
      await we("//set dirt");
      await blockIs(512, "glass");
      // Crossing a protected edge cannot alter its protected half.
      await we("//pos2 513,-60,0");
      await we("//set gold_block");
      await blockIs(512, "glass");
      await we("//pos1 513,-60,0");
      await we("//copy");
      await command("tp MineBodyV1 -1 -60 0");
      await wait(1500);
      assert.equal((await control("resume")).httpStatus, 200);
      // Clipboard anchor moved one block: outside 513 now targets protected 512.
      await we("//paste");
      await blockIs(512, "glass");
      await we("//move 1 west");
      await blockIs(512, "glass");
      await we("//undo");
      await blockIs(512, "glass");
      await command("tp MineBodyV1 0 -60 0");
      await wait(1500);
      assert.equal((await control("resume")).httpStatus, 200);
      console.log(
        "WorldEdit protection passed: outside edit, inclusive boundary, crossing selection, transformed paste, move, undo",
      );
      await command("setworldspawn 1024 -60 0");
    }
    // A real closed shaft has no walking route out. The result must distinguish
    // NoPath from an opaque executor error and carry the actual partial position.
    await command("tp MineBodyV1 0 -60 0");
    await command("fill -1 -60 -1 1 -57 1 stone");
    await command("fill 0 -60 0 0 -57 0 air");
    await wait(1200);
    assert.equal((await control("resume")).httpStatus, 200);
    const noRoute = await send("body_goto", {
      x: 5,
      y: -60,
      z: 0,
      range: 1,
      timeoutSec: 8,
    });
    assert.equal(noRoute.result.ok, false);
    assert.equal(noRoute.result.code, "path_not_found");
    assert.equal(noRoute.result.outcome, "confirmed");
    assert.equal(noRoute.result.data.path.status, "noPath");
    assert.equal(noRoute.result.data.partial, true);
    await command("fill -1 -60 -1 1 -57 1 air");
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
    const correctionStart = Date.now();
    await command("execute as MineBodyV1 at @s run minecraft:tp @s ~0.5 ~ ~");
    await until(async () => {
      const st = await state();
      const event = st.events.findLast(
        (e) => e.type === "forced_move" && e.at >= correctionStart,
      );
      if (!event) return false;
      assert.equal(event.data.correction, true, JSON.stringify(event));
      assert.equal(st.paused, false, "half-block correction must not pause");
      return true;
    }, "real half-block forced move is a collision correction");
    const before = Date.now();
    const stopped = await control("stop");
    assert.equal(stopped.httpStatus, 200, JSON.stringify(stopped));
    assert.ok(Date.now() - before < 2000, "stop should acknowledge within 2s");
    await until(async () => {
      const history = await api("/api/history?conv=" + moving.conv);
      return (
        !history.running && history.messages?.some((m) => /已停止/.test(m.text))
      );
    }, "stop cancels the entire model task");
    const stoppedModelRequests = moving.modelRequests;
    await wait(300);
    assert.equal(
      moving.modelRequests,
      stoppedModelRequests,
      "stopped task does not keep invoking the model",
    );
    if (moving.result) assert.equal(moving.result.ok, false);
    assert.equal((await state()).paused, true);
    assert.equal((await control("resume")).httpStatus, 200);
    const displaced = await send(
      "body_goto",
      { x: 50, y: -60, z: 10, range: 1, timeoutSec: 20 },
      false,
    );
    await until(async () => {
      if (displaced.result) throw Error(JSON.stringify(displaced.result));
      return (await state()).state?.task?.tool === "body_goto";
    }, "active action before large forced move");
    const displacementStart = Date.now();
    await command("execute as MineBodyV1 at @s run minecraft:tp @s ~3 ~ ~");
    await until(async () => {
      const st = await state();
      const event = st.events.findLast(
        (e) => e.type === "forced_move" && e.at >= displacementStart,
      );
      if (!event) return false;
      assert.equal(event.data.correction, false, JSON.stringify(event));
      return st.paused && st.state.pauseReason === "forced_move";
    }, "real three-block forced move pauses");
    assert.equal((await control("stop")).httpStatus, 200);
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
      (
        await api(
          "/api/body/control",
          "POST",
          { action: "resume" },
          profileToken,
        )
      ).httpStatus,
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
  }
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
  const controller = await connectController();
  await command("op BodyController");
  let bodyPanel = await navigate(
    controller,
    await openPanel(controller),
    "AI 玩家",
  );
  assert.ok(
    !bodyPanel.actions.some((a) => a.label.includes("急停")),
    "OP alone must not control body",
  );
  const pairPanel = await navigate(controller, bodyPanel, "关联管理员");
  const pairCode = JSON.stringify(pairPanel).match(/[0-9a-f]{32}/)?.[0];
  assert.ok(pairCode, "pair code displayed only in controller panel");
  await page.goto(origin + "/body#game-code=" + pairCode);
  await until(
    async () =>
      (await page.locator("#body-game-code").inputValue()) === pairCode,
    "authorization code from URL fragment",
  );
  assert.equal(new URL(page.url()).hash, "");
  await page.locator("#body-game-approve").click();
  await page.getByRole("button", { name: "授权此连接", exact: true }).click();
  await page
    .locator("#body-game-result")
    .filter({ hasText: "已授权" })
    .waitFor();
  bodyPanel = await navigate(controller, pairPanel, "刷新身体状态");
  assert.ok(
    bodyPanel.actions.some((a) => a.label.includes("急停")),
    "verified delegation enables native controls",
  );
  let confirmPanel = await navigate(controller, bodyPanel, "恢复");
  bodyPanel = await navigate(controller, confirmPanel, "确认");
  if (uxModel) {
    const { runExperience } = await import("./body-ux-experience.mjs");
    await runExperience({
      page,
      origin,
      api,
      state,
      control,
      command,
      controller,
      until,
      wait,
      temp,
      uxModelMetrics,
    });
  }
  if (!uxOnly) {
    let taskPanel = await navigate(controller, bodyPanel, "下达任务");
    const observed = await bodyTask(controller, taskPanel, "body_observe", {});
    await until(
      () => observed.scenario.result,
      "panel-to-agent-to-body observe",
      30000,
    );
    confirmed(observed.scenario);
    taskPanel = await navigate(controller, observed.dialog, "刷新身体状态");
    assert.ok(
      JSON.stringify(taskPanel).includes("fixture finished"),
      "private result returns to native task panel",
    );
    await command("tp MineBodyV1 0 -60 0");
    await command("setblock 2 -60 2 minecraft:air");
    await wait(1200);
    bodyPanel = await navigate(controller, taskPanel, "返回 AI 玩家");
    confirmPanel = await navigate(controller, bodyPanel, "恢复");
    bodyPanel = await navigate(controller, confirmPanel, "确认");
    taskPanel = await navigate(controller, bodyPanel, "下达任务");
    const proposed = await bodyTask(controller, taskPanel, "body_place", {
      x: 2,
      y: -60,
      z: 2,
      item: "tnt",
      reason: "isolated game panel approval",
    });
    await pending();
    bodyPanel = await navigate(controller, proposed.dialog, "返回 AI 玩家");
    const approvalList = await navigate(controller, bodyPanel, "动作审批");
    const detail = await navigate(controller, approvalList, "查看动作");
    const decided = await navigate(controller, detail, "批准本次动作");
    await until(
      () => proposed.scenario.result,
      "native approval resumes original agent task",
      30000,
    );
    confirmed(proposed.scenario);
    bodyPanel = await navigate(controller, decided, "返回 AI 玩家");
    taskPanel = await navigate(controller, bodyPanel, "下达任务");
    const panelMoving = await bodyTask(controller, taskPanel, "body_goto", {
      x: 20,
      y: -60,
      z: 0,
      range: 1,
    });
    await until(async () => {
      const s = await state();
      return s.state?.task?.tool === "body_goto" && s.state.pos[0] > 3;
    }, "body moving under panel task");
    bodyPanel = await navigate(controller, panelMoving.dialog, "返回 AI 玩家");
    const stopStarted = Date.now();
    await navigate(controller, bodyPanel, "急停并暂停");
    await until(
      async () => (await state()).paused,
      "native emergency stop confirmed",
    );
    assert.ok(
      Date.now() - stopStarted < 3000,
      "native emergency stop bypasses active model/action work",
    );
    controller.quit();
    await wait(700);
    const rejoined = await connectController();
    const freshPanel = await navigate(
      rejoined,
      await openPanel(rejoined),
      "AI 玩家",
    );
    assert.ok(
      !freshPanel.actions.some((a) => a.label.includes("急停")),
      "rejoin does not inherit delegated control",
    );
    rejoined.quit();
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
    assert.equal((await control("resume")).httpStatus, 200);
    const selfStop = await send("body_stop", {}, false);
    await until(async () => {
      const history = await api("/api/history?conv=" + selfStop.conv);
      return (
        !history.running &&
        (selfStop.result ||
          history.messages?.some((m) => /已停止/.test(m.text)))
      );
    }, "body_stop completes without pausing its task");
    assert.equal((await state()).paused, false);
    if (selfStop.result) confirmed(selfStop);
    if (worldEditJar) {
      // Bypass Go/Node tool checks using a real, non-OP game client with the
      // body's identity. Bukkit itself must reject protected target writes.
      assert.equal((await control("disconnect")).httpStatus, 200);
      await until(
        async () => !(await state()).state.connected,
        "body leaves before raw client guard check",
      );
      await wait(700);
      await command("setworldspawn 0 -60 0");
      const raw = mineflayer.createBot({
        host: "127.0.0.1",
        port: 25666,
        username: "MineBodyV1",
        auth: "offline",
        version: "26.1",
      });
      bots.push(raw);
      raw.on("error", () => {});
      await until(() => raw.entity, "raw guard client spawn", 30000);
      await raw.waitForChunksToLoad();
      await command("gamemode creative MineBodyV1");
      await command("tp MineBodyV1 0 -60 0");
      await command("give MineBodyV1 oak_planks 16");
      await command("setblock 1 -60 0 stone");
      await command("setblock 2 -60 0 air");
      await wait(1000);
      const actualBlock = async (x, y, z, block) => {
        const marker = "block-guard-" + randomUUID();
        await command(
          `execute if block ${x} ${y} ${z} minecraft:${block} run say ${marker}`,
        );
        await until(
          () => logs.paper.includes("[Server] " + marker),
          "server block guard " + block,
        );
      };
      await Promise.race([
        raw.dig(raw.blockAt(new Vec3(1, -60, 0))).catch(() => {}),
        wait(1800),
      ]);
      raw.stopDigging();
      await actualBlock(1, -60, 0, "stone");
      await raw.equip(
        raw.inventory.items().find((i) => i.name === "oak_planks"),
        "hand",
      );
      await Promise.race([
        raw
          .placeBlock(raw.blockAt(new Vec3(2, -61, 0)), new Vec3(0, 1, 0))
          .catch(() => {}),
        wait(1800),
      ]);
      await actualBlock(2, -60, 0, "air");
      await command("tp MineBodyV1 513 -60 0");
      await command("setblock 514 -60 0 stone");
      await command("setblock 515 -60 0 air");
      await wait(1200);
      await Promise.race([
        raw.dig(raw.blockAt(new Vec3(514, -60, 0))).catch(() => {}),
        wait(1800),
      ]);
      raw.stopDigging();
      await actualBlock(514, -60, 0, "air");
      await raw.equip(
        raw.inventory.items().find((i) => i.name === "oak_planks"),
        "hand",
      );
      await Promise.race([
        raw
          .placeBlock(raw.blockAt(new Vec3(515, -61, 0)), new Vec3(0, 1, 0))
          .catch(() => {}),
        wait(1800),
      ]);
      await actualBlock(515, -60, 0, "oak_planks");
      raw.quit();
      console.log(
        "Raw client protection passed: protected break/place rejected, outside break/place allowed",
      );
    }
  }
  console.log(
    JSON.stringify({
      passed: true,
      mode: uxOnly
        ? process.env.MINEAGENT_UX_RECHECK === "1"
          ? "real-model-ux-mc-build-recheck"
          : "real-model-ux"
        : "full-regression",
      tools: uxOnly ? undefined : 13,
      modelCalls: calls,
      checks: uxOnly
        ? [
            "real model MC public/verified chat",
            "real model /ai/main tasks; timings are in ux-experience.json",
          ]
        : [
            "real Paper world identity",
            "per-user authenticated tool context",
            "all core actions",
            "real closed terrain returns path_not_found with partial position and search stats",
            "walk outside edit allowlist and inside protected spawn",
            "body dig/place preflight and raw game packet protection",
            "40 actual body observations in one model/tool task",
            "resume while a paused task is planning does not stale its reads",
            "portal approval and resume",
            "stale target rejection",
            "stop during movement",
            "profile scope/privacy",
            "mobile control UI and SSE",
            "native body panel with verified web pairing",
            "native task to real agent/body and private reply",
            "native high-risk approval and emergency stop",
            "same-name reconnect revokes game delegation",
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
  try {
    console.error("fixture state:\n" + redact(JSON.stringify(await state())));
  } catch {
    /* Backend may already be stopped. */
  }
  for (const [kind, text] of Object.entries(logs))
    console.error(kind + ":\n" + redact(text.slice(-6000)));
  throw e;
} finally {
  for (const bot of bots) bot.quit();
  if (browser) await browser.close();
  if (body && body.exitCode === null && body.signalCode === null)
    body.kill("SIGTERM");
  if (proxy && proxy.exitCode === null && proxy.signalCode === null)
    proxy.kill("SIGTERM");
  if (paper && paper.exitCode === null && paper.signalCode === null)
    paper.stdin.end("stop\n");
  await Promise.all(
    children
      .filter((c) => c !== backend)
      .map(
        (c) =>
          new Promise((resolve) => {
            if (c.exitCode !== null || c.signalCode !== null) return resolve();
            c.once("exit", resolve);
            setTimeout(() => {
              if (c.exitCode === null && c.signalCode === null)
                c.kill("SIGTERM");
            }, 10000).unref();
          }),
      ),
  );
  if (backend && backend.exitCode === null && backend.signalCode === null)
    backend.kill("SIGTERM");
  await new Promise((r) => model.close(r));
}
