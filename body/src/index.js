import { loadConfig } from "./config.js";
import { MinecraftBody, capabilities } from "./minecraft.js";
const args = process.argv.slice(2);
if (args.length !== 2 || args[0] !== "--config") {
  console.error("Usage: node src/index.js --config <private config.json>");
  process.exit(1);
}
let config;
try {
  config = loadConfig(args[1]);
} catch (e) {
  console.error(e.message);
  process.exit(1);
}
let ws,
  stopping = false,
  reconnect,
  backoff = 5000;
const log = (type, data = {}) =>
  console.log(JSON.stringify({ at: new Date().toISOString(), type, ...data }));
const body = new MinecraftBody(config, (type, data) => {
  if (ws?.readyState !== WebSocket.OPEN) return;
  const message = JSON.stringify({ v: 1, type, ts: Date.now(), data });
  if (Buffer.byteLength(message) > 16384) {
    log("message_too_large", { messageType: type });
    body.pause("message_too_large");
    return;
  }
  ws.send(message);
});
function connect() {
  if (stopping) return;
  const conn = new WebSocket(config.backend.url);
  ws = conn;
  conn.addEventListener("open", () =>
    conn.send(
      JSON.stringify({
        v: 1,
        type: "hello",
        data: {
          protocol: 1,
          role: "minecraft-body",
          plugin: "mineagent-body",
          pluginVersion: "0.1.0",
          serverVersion: config.minecraft.targetVersion,
          token: config.backend.token,
          bodyId: config.backend.bodyId,
          bodyName: config.minecraft.username,
          bodyProtocolVersion: 1,
          clientMcVersion: config.minecraft.version,
          targetMcVersion: config.minecraft.targetVersion,
          viaProxy: !!config.minecraft.viaProxy,
          capabilities,
        },
      }),
    ),
  );
  conn.addEventListener("message", (event) => {
    if (
      ws !== conn ||
      typeof event.data !== "string" ||
      Buffer.byteLength(event.data) > 16384
    )
      return;
    try {
      const e = JSON.parse(event.data);
      if (e.v !== 1) throw Error("unsupported_protocol");
      switch (e.type) {
        case "hello_ack":
          backoff = 5000;
          log("backend_connected");
          break;
        case "ping":
          conn.send(JSON.stringify({ v: 1, type: "pong" }));
          break;
        case "body.policy":
          body.applyPolicy(e.data);
          break;
        case "body.lease":
          body.lease(e.data);
          break;
        case "body.inspect":
          body.inspect(e.data);
          break;
        case "body.call":
          void body.call(e.data).catch(() => body.pause("executor_failed"));
          break;
        case "body.control":
          void body.control(e.data).catch(() => body.pause("control_failed"));
          break;
        default:
          throw Error("unsupported_message");
      }
    } catch {
      log("protocol_rejected");
      body.backendLost();
      conn.close();
    }
  });
  conn.addEventListener("error", () => log("backend_connection_error"));
  conn.addEventListener("close", () => {
    if (ws !== conn) return;
    body.backendLost();
    if (!stopping) {
      log("backend_disconnected");
      reconnect = setTimeout(connect, backoff + Math.random() * 500);
      backoff = Math.min(60000, backoff * 2);
    }
  });
}
connect();
async function stop() {
  if (stopping) return;
  stopping = true;
  clearTimeout(reconnect);
  await body.close();
  ws?.close();
  setTimeout(() => process.exit(0), 500).unref();
}
process.on("SIGTERM", stop);
process.on("SIGINT", stop);
