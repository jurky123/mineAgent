// Only run against a marked disposable Paper directory; never production config or player data.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import os from "node:os";
import { spawn } from "node:child_process";
import { createRequire } from "node:module";
import { randomBytes } from "node:crypto";
const require = createRequire(new URL("../body/package.json", import.meta.url));
const mineflayer = require("mineflayer"),
  nbt = require("prismarine-nbt");
const { WebSocketServer } = require("ws");
const [paperDir, proxyJar] = process.argv.slice(2);
assert.ok(
  paperDir && proxyJar,
  "Usage: node scripts/paper-ui-e2e.mjs <marked test Paper directory> <ViaProxy jar>",
);
assert.ok(
  (
    await fs.readFile(path.join(paperDir, ".mineagent-body-test"), "utf8")
  ).includes("isolated test"),
);
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
    "test Paper must use " + entry,
  );
const repo = process.cwd(),
  temp = await fs.mkdtemp(path.join(os.tmpdir(), "mineagent-paper-ui-"));
await fs.chmod(temp, 0o700);
const fixtureToken = randomBytes(24).toString("hex");
const children = [],
  bots = [],
  logs = { paper: "", proxy: "" };
let paper, peer, server;
const messages = [];
const pause = (ms) => new Promise((r) => setTimeout(r, ms));
async function until(fn, name, ms = 20000) {
  const started = Date.now();
  let last;
  while (Date.now() - started < ms) {
    try {
      const r = await fn();
      if (r) return r;
    } catch (e) {
      last = e;
    }
    await pause(60);
  }
  throw Error(name + (last ? ": " + last.message : ""));
}
function start(kind, cmd, args, cwd) {
  const child = spawn(cmd, args, {
    cwd,
    env: { PATH: process.env.PATH, HOME: process.env.HOME, LANG: "C.UTF-8" },
    stdio: ["pipe", "pipe", "pipe"],
  });
  children.push(child);
  for (const s of [child.stdout, child.stderr])
    s.on("data", (b) => (logs[kind] = (logs[kind] + b).slice(-80000)));
  return child;
}
function send(type, data) {
  assert.ok(peer);
  peer.send(JSON.stringify({ v: 1, type, data, ts: Date.now() }));
}
async function command(text) {
  paper.stdin.write(text + "\n");
  await pause(250);
}
async function connect(name) {
  const bot = mineflayer.createBot({
    host: "127.0.0.1",
    port: 25666,
    username: name,
    auth: "offline",
    version: "26.1",
  });
  bots.push(bot);
  bot.dialogs = [];
  bot.dialogPackets = [];
  bot.ui = [];
  bot._client.on("packet", (_packet, meta) => {
    if (meta.name === "show_dialog" || meta.name === "clear_dialog")
      bot.dialogPackets.push(meta.name);
  });
  bot.on("error", () => {});
  bot._client.on("show_dialog", (p) =>
    bot.dialogs.push({ raw: p.dialog.data, data: nbt.simplify(p.dialog.data) }),
  );
  bot._client.on("custom_payload", (p) => {
    if (p.channel !== "mineui:main" || p.data.length < 17) return;
    const d = p.data;
    bot.ui.push({
      type: d[3],
      session: d.readInt32BE(4),
      revision: d.readInt32BE(8),
      data: d.length > 17 ? JSON.parse(d.subarray(17).toString()) : {},
    });
  });
  await until(() => bot.entity, "player spawn " + name, 30000);
  await pause(1000);
  return bot;
}
async function openDialog(bot, cmd = "/agent") {
  const old = bot.dialogs.length;
  const packets = bot.dialogPackets.length;
  bot.chat(cmd);
  const dialog = await until(() => bot.dialogs[old], "dialog " + cmd);
  assert.deepEqual(bot.dialogPackets.slice(packets), ["show_dialog"],
    "native dialog opening must not send a preceding clear-dialog packet");
  return dialog;
}
function dialogButton(dialog, label) {
  const b = [...(dialog.data.actions || []), dialog.data.exit_action]
    .filter(Boolean)
    .find((b) => typeof b.label === "string" && b.label.includes(label));
  assert.ok(b, "missing dialog button " + label);
  return b.action;
}
function varint(value) {
  const bytes = [];
  do {
    let b = value & 127;
    value >>>= 7;
    if (value) b |= 128;
    bytes.push(b);
  } while (value);
  return Buffer.from(bytes);
}
function clickDialog(bot, action, text) {
  const payload = { id: nbt.intArray(action.additions.id) };
  if (text !== undefined) payload.question = nbt.string(text);
  // The pinned minecraft-data schema omits the common packet's bounded NBT length.
  // Encode this test-only action as Paper's ServerboundCustomClickActionPacket codec:
  // identifier + length-prefixed anonymous NBT, not a boolean-prefixed option.
  const named = nbt.writeUncompressed(nbt.comp(payload)),
    tag = Buffer.concat([named.subarray(0, 1), named.subarray(3)]),
    id = Buffer.from(action.id);
  const mappings =
    require("minecraft-data")("26.1").protocol.play.toServer.types.packet[1][0]
      .type[1].mappings;
  const packetId = Number(
    Object.entries(mappings).find(
      ([, name]) => name === "custom_click_action",
    )[0],
  );
  bot._client.writeRaw(
    Buffer.concat([
      varint(packetId),
      varint(id.length),
      id,
      varint(tag.length),
      tag,
    ]),
  );
}
async function navigate(bot, dialog, label) {
  const count = bot.dialogs.length;
  clickDialog(bot, dialogButton(dialog, label));
  return until(() => bot.dialogs[count], "click " + label);
}
function uiFrame(bot, type, session, revision, data) {
  const payload = Buffer.from(JSON.stringify(data)),
    frame = Buffer.alloc(17 + payload.length);
  frame.writeUInt16BE(0x4d55, 0);
  frame[2] = 1;
  frame[3] = type;
  frame.writeInt32BE(session, 4);
  frame.writeInt32BE(revision, 8);
  frame.writeInt32BE(payload.length, 13);
  payload.copy(frame, 17);
  bot._client.write("custom_payload", { channel: "mineui:main", data: frame });
}
function currentUi(bot) {
  const open = bot.ui.findLast((m) => m.type === 3);
  if (!open) return null;
  const snapshot = bot.ui.findLast(
    (m) => m.type === 5 && m.session === open.session,
  );
  if (!snapshot) return null;
  let state = structuredClone(snapshot.data.state),
    revision = snapshot.revision;
  const start = bot.ui.indexOf(snapshot);
  for (const m of bot.ui.slice(start + 1)) {
    if (m.session !== open.session || m.type !== 6) continue;
    revision = m.revision;
    for (const op of m.data.ops || []) {
      const keys = op.path
        .slice(1)
        .split("/")
        .map((k) => k.replaceAll("~1", "/").replaceAll("~0", "~"));
      let v = state;
      for (const k of keys.slice(0, -1)) v = v[k] ??= {};
      v[keys.at(-1)] = op.value;
    }
  }
  return {
    session: open.session,
    revision,
    state,
    definition: open.data.ui ?? open.data.definition,
  };
}
async function openUi(bot) {
  const count = bot.ui.length;
  bot.chat("/agent ui");
  return until(
    () => bot.ui.slice(count).some((m) => m.type === 5) && currentUi(bot),
    "enhanced UI",
  );
}
async function uiAction(bot, id, payload = {}) {
  const ui = currentUi(bot);
  uiFrame(bot, 7, ui.session, ui.revision, { id, payload });
  await pause(200);
}
async function startPaper() {
  logs.paper = "";
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
    "Paper ready",
    60000,
  );
}
function approval(id, timeoutMs = 30000) {
  send("approval.request", {
    approvalId: id,
    tool: "minecraft_give",
    requester: "fixture-player",
    prompt: "isolated approval fixture",
    timeoutMs,
  });
}
const decisionCount = () => messages.filter((m) => m.approval).length;
async function stopPaper() {
  for (const bot of bots.splice(0)) bot.quit();
  if (paper && paper.exitCode === null) {
    paper.stdin.end("stop\n");
    await new Promise((r) => paper.once("exit", r));
  }
  peer = null;
}
try {
  server = new WebSocketServer({ host: "127.0.0.1", port: 0 });
  await new Promise((r) => server.once("listening", r));
  server.on("connection", (ws) => {
    peer = ws;
    const ping = setInterval(() => {
      if (ws.readyState === 1)
        ws.send(JSON.stringify({ v: 1, type: "ping", data: {} }));
    }, 5000);
    ws.on("close", () => clearInterval(ping));
    ws.on("message", (raw) => {
      const m = JSON.parse(raw);
      if (m.type === "hello")
        ws.send(
          JSON.stringify({
            v: 1,
            type: "hello_ack",
            data: { protocol: 1, backendVersion: "ui-fixture" },
          }),
        );
      else if (m.type === "chat.message") {
        messages.push(m.data);
        ws.send(
          JSON.stringify({
            v: 1,
            type: "agent.message",
            data: {
              text: "fixture reply: " + m.data.message,
              target: m.data.player,
            },
          }),
        );
      } else if (m.type === "approval.result")
        messages.push({ approval: m.data });
    });
  });
  await fs.mkdir(path.join(paperDir, "plugins/MineAgent"), { recursive: true });
  for (const file of await fs.readdir(path.join(paperDir, "plugins")))
    if (/^MineAgent.*\.jar$/.test(file))
      await fs.unlink(path.join(paperDir, "plugins", file));
  const version = (
    await fs.readFile(path.join(repo, "paper-plugin/gradle.properties"), "utf8")
  ).match(/^version=(.+)$/m)[1];
  const jar = `MineAgent-${version}.jar`;
  await fs.copyFile(
    path.join(repo, "paper-plugin/build/libs", jar),
    path.join(paperDir, "plugins", jar),
  );
  const mineUiFile = path.join(paperDir, "plugins/MineUI-ui-fixture.jar");
  await fs.rm(mineUiFile, { force: true });
  await fs.writeFile(
    path.join(paperDir, "plugins/MineAgent/config.yml"),
    `backend:\n  url: "ws://127.0.0.1:${server.address().port}/ws"\n  token: "${fixtureToken}"\n  heartbeat-ms: 10000\nui:\n  mode: auto\n`,
    { mode: 0o600 },
  );
  start(
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
  await until(() => logs.proxy.includes("25666"), "proxy ready", 30000);
  await startPaper();
  const guest = await connect("MineUiGuest"),
    admin = await connect("MineUiAdmin");
  await command("op MineUiAdmin");
  const home = await openDialog(guest);
  assert.ok(JSON.stringify(home.data).includes("向助手提问"));
  assert.ok(!JSON.stringify(home.data).includes("待审批"));
  const chat = await navigate(guest, home, "向助手提问");
  clickDialog(guest, dialogButton(chat, "发送问题"), "native-ui-question");
  await until(
    () => messages.some((m) => m.message === "@agent native-ui-question"),
    "native input routed to backend",
  );
  const adminHistory = await navigate(
    admin,
    await openDialog(admin),
    "本次在线记录",
  );
  assert.ok(
    !JSON.stringify(adminHistory.data).includes("fixture reply:"),
    "targeted reply must stay private",
  );
  approval("ap-native-1");
  await pause(200);
  const list = await navigate(admin, await openDialog(admin), "待审批");
  const detail = await navigate(admin, list, "查看请求");
  const approve = dialogButton(detail, "批准本次");
  clickDialog(admin, approve);
  await until(
    () => messages.some((m) => m.approval?.approvalId === "ap-native-1"),
    "native approval dispatch",
  );
  const count = decisionCount();
  clickDialog(admin, approve);
  await pause(200);
  assert.equal(decisionCount(), count, "one-use callback");
  approval("ap-forged");
  await pause(100);
  const forgedDetail = await navigate(
    admin,
    await navigate(admin, await openDialog(admin), "待审批"),
    "查看请求",
  );
  clickDialog(guest, dialogButton(forgedDetail, "批准本次"));
  await pause(200);
  assert.equal(decisionCount(), count, "callback owner binding");
  await command("deop MineUiAdmin");
  const denied = await openDialog(admin);
  assert.ok(!JSON.stringify(denied.data).includes("待审批"));
  await command("op MineUiAdmin");
  // A button displayed while authorized cannot be used after permission revocation.
  const revokedDetail = await navigate(
    admin,
    await navigate(admin, await openDialog(admin), "待审批"),
    "查看请求",
  );
  await command("deop MineUiAdmin");
  clickDialog(admin, dialogButton(revokedDetail, "批准本次"));
  await pause(200);
  assert.equal(decisionCount(), count);
  await command("op MineUiAdmin");
  approval("ap-expired", 2000);
  await pause(150);
  const expiredDetail = await navigate(
    admin,
    await navigate(admin, await openDialog(admin), "待审批"),
    "查看请求",
  );
  const expiredButton = dialogButton(expiredDetail, "批准本次");
  await pause(2200);
  clickDialog(admin, expiredButton);
  await pause(200);
  assert.equal(decisionCount(), count, "expired approval rejected");
  const previous = peer;
  peer.terminate();
  await until(() => peer && peer !== previous, "backend reconnect");
  await pause(250);
  const reconnected = await openDialog(admin);
  assert.ok(
    JSON.stringify(reconnected.data).includes("待审批 · 0"),
    "reconnect clears prior backend requests",
  );
  console.log(
    "PASS: vanilla dialog input, private history, approval replay/actor/permission guards",
  );
  await stopPaper();
  const mineUiJar = path.join(
    os.homedir(),
    ".m2/repository/com/mineui/mineui-paper/0.16.1/mineui-paper-0.16.1.jar",
  );
  await fs.copyFile(mineUiJar, mineUiFile);
  await startPaper();
  const enhancedGuest = await connect("MineUiGuest"),
    enhancedAdmin = await connect("MineUiAdmin");
  await command("op MineUiAdmin");
  // MineUI is installed, but a vanilla client still gets the native dialog.
  assert.ok(
    JSON.stringify((await openDialog(enhancedGuest)).data).includes(
      "向助手提问",
    ),
  );
  for (const cmd of ["/agent dialog", "/mineagent:agent", "/mineagent:agent dialog", "/agent ui", "/ai", "/mineagent ui"])
    assert.ok(JSON.stringify((await openDialog(enhancedGuest, cmd)).data).includes("向助手提问"), cmd);
  enhancedGuest._client.write("custom_payload", {
    channel: "minecraft:register", data: Buffer.from("mineui:main"),
  });
  uiFrame(enhancedGuest, 1, 0, 0, {
    protocol: 1, modVersion: "0.6.4", minecraft: "26.2", capabilities: ["server_ui"],
  });
  await until(() => enhancedGuest.ui.some(m => m.type === 2), "old client handshake");
  assert.ok(JSON.stringify((await openDialog(enhancedGuest)).data).includes("向助手提问"), "old MineUI clients must fall back");
  for (const bot of [enhancedGuest, enhancedAdmin]) {
    bot._client.write("custom_payload", {
      channel: "minecraft:register",
      data: Buffer.from("mineui:main"),
    });
    uiFrame(bot, 1, 0, 0, {
      protocol: 1,
      modVersion: "0.16.1",
      minecraft: "26.2",
      capabilities: ["server_ui"],
    });
  }
  await until(
    () => enhancedGuest.ui.some((m) => m.type === 2),
    "MineUI handshake",
  );
  assert.ok(JSON.stringify((await openDialog(enhancedGuest)).data).includes("向助手提问"),
    "default /agent stays native even for an enhanced client");
  const enhancedHome = await openUi(enhancedGuest);
  assert.equal(enhancedHome.state.title, "MineAgent · 助手");
  await uiAction(enhancedGuest, "chat");
  await until(
    () => currentUi(enhancedGuest)?.state.title === "与助手对话",
    "enhanced chat",
  );
  await uiAction(enhancedGuest, "send", { text: "enhanced-ui-question" });
  await until(
    () => messages.some((m) => m.message === "@agent enhanced-ui-question"),
    "enhanced input routed",
  );
  await until(
    () =>
      JSON.stringify(currentUi(enhancedGuest).state).includes("fixture reply:"),
    "live reply patch",
  );
  assert.ok(
    !JSON.stringify((await openUi(enhancedAdmin)).state).includes(
      "fixture reply:",
    ),
    "enhanced private state",
  );
  const prior = decisionCount();
  await uiAction(enhancedGuest, "approve");
  await uiAction(enhancedGuest, "body_stop");
  assert.equal(
    decisionCount(),
    prior,
    "unregistered privilege actions ignored",
  );
  approval("ap-enhanced");
  await pause(150);
  await openUi(enhancedAdmin);
  await uiAction(enhancedAdmin, "approvals");
  await until(
    () => currentUi(enhancedAdmin)?.state.title === "操作审批",
    "enhanced approval list",
  );
  await uiAction(enhancedAdmin, "request:ap-enhanced");
  await uiAction(enhancedAdmin, "approve");
  await until(
    () => messages.some((m) => m.approval?.approvalId === "ap-enhanced"),
    "enhanced approval dispatch",
  );
  // Opening a new page invalidates an old MineUI session even if the client replays it.
  const old = currentUi(enhancedGuest),
    before = messages.filter((m) => m.message).length;
  await openUi(enhancedGuest);
  uiFrame(enhancedGuest, 7, old.session, old.revision, {
    id: "send",
    payload: { text: "must-not-send" },
  });
  await pause(250);
  assert.equal(messages.filter((m) => m.message).length, before);
  console.log(
    JSON.stringify({
      passed: true,
      checks: [
        "vanilla without MineUI",
        "native form callbacks",
        "targeted history privacy",
        "one-use/actor/revoked approval guards",
        "vanilla with MineUI installed",
      "old MineUI client fallback",
        "MineUI handshake/open/input/live patches",
        "enhanced approvals",
        "stale sessions",
        "unknown privilege actions rejected",
      ],
      artifacts: temp,
    }),
  );
} catch (e) {
  for (const [k, v] of Object.entries(logs))
    console.error(
      k + ":\n" + v.slice(-4000).replaceAll(fixtureToken, "<fixture-token>"),
    );
  throw e;
} finally {
  await stopPaper();
  for (const child of children)
    if (child.exitCode === null) child.kill("SIGTERM");
  if (server) {
    for (const ws of server.clients) ws.terminate();
    await new Promise((r) => server.close(r));
  }
}
