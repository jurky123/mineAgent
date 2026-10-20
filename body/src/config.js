import fs from "node:fs";
import net from "node:net";
import mineflayer from "mineflayer";
export function validateConfig(input, env = process.env) {
  const c = structuredClone(input);
  const b = c.backend || {},
    mc = c.minecraft || {};
  const local = (host) =>
    host === "127.0.0.1" || host === "::1" || host === "[::1]";
  const u = new URL(b.url || "ws://127.0.0.1:8765/ws");
  if (
    u.protocol !== "ws:" ||
    !local(u.hostname) ||
    u.username ||
    u.password ||
    u.search ||
    u.hash ||
    u.pathname !== "/ws"
  )
    throw Error("backend must be a loopback ws URL without credentials/query");
  const token = env[b.tokenEnv || "MINEAGENT_BODY_TOKEN"];
  if (!token || token.length < 32)
    throw Error("independent body token is required (at least 32 characters)");
  if (
    !local(mc.host) ||
    !net.isIP(mc.host.replaceAll("[", "").replaceAll("]", "")) ||
    !Number.isInteger(mc.port) ||
    mc.port < 1024 ||
    mc.port > 65535
  )
    throw Error("Minecraft target must be an explicit loopback host/port");
  if (!/^[A-Za-z0-9_]{3,16}$/.test(mc.username || "") || mc.auth !== "offline")
    throw Error("invalid body name/auth");
  if (!mineflayer.testedVersions.includes(mc.version))
    throw Error("unsupported Minecraft client version");
  if (!mc.targetVersion) throw Error("explicit targetVersion required");
  return {
    backend: { url: u.href, bodyId: b.bodyId || "main", token },
    minecraft: mc,
  };
}
export function loadConfig(path) {
  return validateConfig(JSON.parse(fs.readFileSync(path, "utf8")));
}
