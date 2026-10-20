import mineflayer from "mineflayer";
import pathfinder from "mineflayer-pathfinder";
import { Vec3 } from "vec3";
import { randomUUID } from "node:crypto";
import { performance } from "node:perf_hooks";
import {
  risk,
  checkSpec,
  checkGrant,
  fail,
  nameOf,
  readTools,
  safeFoods,
  vector,
  forbiddenText,
} from "./policy.js";
const faces = {
  up: [0, 1, 0],
  down: [0, -1, 0],
  north: [0, 0, -1],
  south: [0, 0, 1],
  east: [1, 0, 0],
  west: [-1, 0, 0],
};
const pos = (v) => [v.x, v.y, v.z];
const distance = (a, b) => Math.hypot(...a.map((n, i) => n - b[i]));
const freeze = (o) => {
  if (o && typeof o === "object") {
    Object.values(o).forEach(freeze);
    Object.freeze(o);
  }
  return o;
};
const clean = (x) => String(x || "").slice(0, 500);
export const capabilities = [
  "observe",
  "move",
  "dig",
  "place",
  "interact",
  "item",
  "chat",
  "command",
];

export class MinecraftBody {
  constructor(config, send) {
    this.config = config;
    this.send = send;
    this.bot = null;
    this.policy = null;
    this.connectionId = "";
    this.epoch = 0;
    this.revision = 0;
    this.seq = 0;
    this.ready = false;
    this.connected = false;
    this.paused = true;
    this.disconnected = false;
    this.lastLease = 0;
    this.active = null;
    this.prepared = new Map();
    this.results = new Map();
    this.usedGrants = new Set();
    this.recent = [];
    this.limits = new Map();
    this.lastReconnect = 5000;
    this.safetyTimer = setInterval(() => {
      if (this.ready && performance.now() - this.lastLease > 1500)
        this.pause("lease_expired");
    }, 100).unref();
    this.stateTimer = setInterval(() => {
      if (this.ready && (this.active || ++this.idleTicks >= 5)) {
        this.idleTicks = 0;
        this.publishState();
      }
    }, 1000).unref();
    this.idleTicks = 0;
  }
  applyPolicy(p) {
    if (
      !p?.connectionId ||
      !Number.isSafeInteger(p.controlEpoch) ||
      !Number.isSafeInteger(p.policyRevision) ||
      p.name !== this.config.minecraft.username ||
      p.bodyId !== this.config.backend.bodyId ||
      p.expectedMcVersion !== this.config.minecraft.targetVersion ||
      !["off", "observe", "assist"].includes(p.mode) ||
      !["allowlist", "unrestricted"].includes(p.zones?.mode)
    )
      fail("invalid_policy");
    for (const a of p.zones.areas || [])
      if (
        !vector(a.min) ||
        !vector(a.max) ||
        a.min.some((n, i) => n >= a.max[i])
      )
        fail("invalid_policy");
    this.pause("policy_changed");
    this.policy = structuredClone(p);
    this.connectionId = p.connectionId;
    this.epoch = p.controlEpoch;
    this.revision = p.policyRevision;
    this.seq = 0;
    this.results.clear();
    this.usedGrants.clear();
    this.ready = true;
    this.lastLease = performance.now();
    this.send("body.policy_ack", {
      connectionId: this.connectionId,
      controlEpoch: this.epoch,
      policyRevision: this.revision,
      paused: true,
    });
    if (p.mode !== "off") this.connect();
  }
  lease(p) {
    if (p.connectionId === this.connectionId && p.controlEpoch === this.epoch)
      this.lastLease = performance.now();
  }
  world() {
    const raw = this.bot?.game?.dimension;
    if (!raw) return { worldId: "", dimension: "" };
    const dimension = raw.startsWith("minecraft:") ? raw : "minecraft:" + raw;
    const matches = (this.policy?.worldMappings || []).filter(
      (w) => w.dimension === dimension,
    );
    return {
      worldId: matches.length === 1 ? matches[0].worldId : "",
      dimension,
    };
  }
  connect() {
    if (this.bot || this.disconnected || !this.ready) return;
    const mc = this.config.minecraft;
    const bot = mineflayer.createBot({
      host: mc.host,
      port: mc.port,
      username: mc.username,
      auth: "offline",
      version: mc.version,
      respawn: false,
    });
    this.bot = bot;
    bot.loadPlugin(pathfinder.pathfinder);
    bot.on("spawn", async () => {
      if (this.bot !== bot) return;
      this.paused = true;
      this.connected = false;
      this.onlineSince = Date.now();
      try {
        await bot.waitForChunksToLoad();
        if (this.bot !== bot) return;
        this.connected = true;
        this.lastReconnect = 5000;
        this.movements();
        this.event("spawn");
        this.publishState();
      } catch {
        this.pause("spawn_failed");
      }
    });
    bot.on("death", () => {
      this.pause("death");
      this.event("death");
      setTimeout(() => {
        if (this.bot === bot && !this.disconnected) bot.respawn();
      }, 1000).unref();
    });
    bot.on("respawn", () => this.pause("respawn"));
    bot.on("forcedMove", () => {
      if (
        this.connected &&
        (this.active || Date.now() - this.onlineSince > 2000)
      )
        this.pause("forced_move");
    });
    bot.on("health", () => this.publishState());
    bot.on("chat", (player, message) => {
      if (player !== mc.username)
        this.event("chat", {
          player: clean(player).slice(0, 32),
          message: clean(message),
        });
    });
    bot.on("kicked", (reason) => {
      this.event("kicked", {
        reason: clean(
          typeof reason === "string" ? reason : JSON.stringify(reason),
        ),
      });
      if (/banned|ban reason/i.test(JSON.stringify(reason)))
        this.disconnected = true;
    });
    bot.on("error", () =>
      this.event("error", { code: "minecraft_connection_error" }),
    );
    bot.on("end", () => {
      if (this.bot !== bot) return;
      this.pause("minecraft_disconnected");
      this.connected = false;
      this.bot = null;
      this.onlineSince = null;
      this.event("offline");
      this.publishState();
      this.scheduleReconnect();
    });
  }
  scheduleReconnect() {
    clearTimeout(this.reconnectTimer);
    if (this.disconnected || !this.ready) return;
    const ms = this.lastReconnect + Math.random() * 500;
    this.lastReconnect = Math.min(60000, this.lastReconnect * 2);
    this.reconnectTimer = setTimeout(() => this.connect(), ms);
    this.reconnectTimer.unref();
  }
  movements() {
    const bot = this.bot;
    if (!bot?.entity) return;
    const m = new pathfinder.Movements(bot);
    m.canDig = false;
    m.allow1by1towers = false;
    m.scafoldingBlocks = [];
    m.canOpenDoors = false;
    m.allowParkour = false;
    m.allowSprinting = false;
    m.maxDropDown = 3;
    bot.pathfinder.setMovements(m);
    bot.pathfinder.searchRadius = 32;
    bot.pathfinder.thinkTimeout = 3000;
    bot.pathfinder.tickTimeout = 10;
    bot.pathfinder.enablePathShortcut = false;
  }
  envelope(p) {
    if (
      !this.ready ||
      p.connectionId !== this.connectionId ||
      p.controlEpoch !== this.epoch ||
      p.policyRevision !== this.revision ||
      performance.now() - this.lastLease > 1500
    )
      fail("stale");
  }
  stopInputs() {
    const b = this.bot;
    if (!b) return;
    try {
      b.pathfinder.setGoal(null);
    } catch {}
    try {
      b.stopDigging();
    } catch {}
    try {
      b.clearControlStates();
    } catch {}
    try {
      b.deactivateItem();
    } catch {}
    if (b.currentWindow)
      try {
        b.closeWindow(b.currentWindow);
      } catch {}
  }
  cancel(reason) {
    if (this.active) {
      this.active.reason = reason;
      this.active.abort.abort();
    }
    this.stopInputs();
  }
  pause(reason) {
    const changed = !this.paused;
    this.paused = true;
    this.prepared.clear();
    this.cancel(reason);
    if (changed && this.ready) {
      this.event("paused", { code: reason });
      this.publishState();
    }
  }
  backendLost() {
    this.pause("backend_disconnected");
    this.ready = false;
    clearTimeout(this.reconnectTimer);
  }
  async control(p) {
    if (
      !this.ready ||
      p.connectionId !== this.connectionId ||
      !Number.isSafeInteger(p.controlEpoch) ||
      p.controlEpoch <= this.epoch
    )
      return;
    this.pause("control");
    this.epoch = p.controlEpoch;
    this.lastLease = performance.now();
    const old = this.active;
    if (old)
      await Promise.race([old.done, new Promise((r) => setTimeout(r, 1500))]);
    if (p.action === "disconnect") {
      this.disconnected = true;
      clearTimeout(this.reconnectTimer);
      this.connected = false;
      this.bot?.quit();
    } else if (p.action === "resume" && !this.active) {
      this.disconnected = false;
      if (!this.bot) {
        this.paused = true;
        this.connect();
      } else if (this.connected) this.paused = false;
    }
    this.publishState();
    this.send("body.control_ack", {
      id: p.id,
      connectionId: this.connectionId,
      controlEpoch: this.epoch,
      policyRevision: this.revision,
      paused: this.paused,
    });
  }
  state() {
    const b = this.bot;
    return {
      connectionId: this.connectionId,
      controlEpoch: this.epoch,
      policyRevision: this.revision,
      seq: ++this.seq,
      connected: this.connected,
      paused: this.paused,
      name: this.config.minecraft.username,
      ...this.world(),
      pos: b?.entity ? pos(b.entity.position) : [0, 0, 0],
      yaw: b?.entity?.yaw || 0,
      pitch: b?.entity?.pitch || 0,
      health: b?.health || 0,
      food: b?.food || 0,
      gameMode: b?.game?.gameMode || "",
      heldItem: b?.heldItem?.name || "",
      task: this.active
        ? {
            id: this.active.callId,
            tool: this.active.tool,
            startedAt: this.active.startedAt,
          }
        : null,
      updatedAt: Date.now(),
      onlineSince: this.connected ? this.onlineSince : null,
      clientMcVersion: this.config.minecraft.version,
      targetMcVersion: this.config.minecraft.targetVersion,
      viaProxy: !!this.config.minecraft.viaProxy,
    };
  }
  publishState() {
    if (this.ready) this.send("body.state", this.state());
  }
  event(type, data = {}) {
    if (type === "chat") {
      const now = performance.now();
      if (now - (this.lastChat || 0) < 500) return;
      this.lastChat = now;
    }
    const e = { connectionId: this.connectionId, type, at: Date.now(), data };
    this.recent.push(e);
    if (this.recent.length > 50) this.recent.shift();
    if (this.ready) this.send("body.event", e);
  }
  args(a) {
    if (!a || typeof a !== "object" || Array.isArray(a)) fail("invalid_args");
    for (const k of ["x", "y", "z"])
      if (
        a[k] !== undefined &&
        (!Number.isFinite(a[k]) || Math.abs(a[k]) > 30000000)
      )
        fail("invalid_args");
    for (const k of ["range", "radius", "count", "timeoutSec", "holdMs"])
      if (a[k] !== undefined && (!Number.isInteger(a[k]) || a[k] < 0))
        fail("invalid_args");
    if (
      (a.range || 0) > 16 ||
      (a.radius || 0) > 32 ||
      (a.count || 0) > 16 ||
      (a.timeoutSec || 0) > 20 ||
      (a.holdMs || 0) > 2000
    )
      fail("invalid_args");
    return structuredClone(a);
  }
  block(p) {
    const b = this.bot.blockAt(new Vec3(...p));
    if (!b) fail("target_not_loaded");
    return b;
  }
  item(item) {
    const n = nameOf(item || this.bot.heldItem?.name);
    if (!n) fail("no_item");
    const found = this.bot.inventory.items().find((i) => i.name === n);
    if (!found) fail("no_item");
    return found;
  }
  describe(tool, input) {
    const b = this.bot;
    if (!this.connected || !b?.entity) fail("body_offline");
    const args = this.args(input),
      w = this.world();
    if (!w.worldId) fail("world_unknown");
    if (args.player) {
      const e = b.players[args.player]?.entity;
      if (!e) fail("target_not_loaded");
      [args.x, args.y, args.z] = pos(e.position);
      delete args.player;
    }
    if (args.entityId !== undefined) {
      if (tool !== "body_look_at") fail("unsupported_target");
      const e = b.entities[args.entityId];
      if (!e) fail("target_not_loaded");
      [args.x, args.y, args.z] = pos(e.position);
      delete args.entityId;
    }
    const spec = {
      id: randomUUID(),
      tool,
      args,
      connectionId: this.connectionId,
      controlEpoch: this.epoch,
      policyRevision: this.revision,
      expiresAt: Date.now() + 240000,
      ...w,
      pos: pos(b.entity.position),
      block: "",
      blockState: 0,
      support: "",
      supportState: 0,
      mainHand: b.heldItem?.name || "",
      offHand: b.inventory.slots[45]?.name || "",
      slot: -1,
      count: 0,
      hazards: [],
    };
    if (
      [
        "body_goto",
        "body_look_at",
        "body_dig",
        "body_place",
        "body_interact",
      ].includes(tool)
    ) {
      const target = [args.x, args.y, args.z];
      if (!vector(target)) fail("invalid_args");
      spec.target = target;
      const block = this.block(target);
      spec.block = block.name;
      spec.blockState = block.stateId;
    }
    if (
      ["body_dig", "body_place", "body_interact"].includes(tool) &&
      !spec.target.every(Number.isInteger)
    )
      fail("invalid_args");
    if (tool === "body_place") {
      const item = this.item(args.item);
      if (!b.registry.blocksByName[item.name]) fail("unsupported_item");
      args.item = item.name;
      spec.slot = item.slot;
      spec.count = item.count;
      if (!["air", "cave_air", "void_air"].includes(spec.block))
        fail("target_occupied");
      const available = args.face ? [args.face] : Object.keys(faces);
      let found = false;
      for (const f of available) {
        const face = faces[f];
        if (!face) fail("invalid_args");
        const ref = spec.target.map((n, i) => n - face[i]);
        const support = this.block(ref);
        if (support.boundingBox === "empty") continue;
        spec.reference = ref;
        spec.faceVector = face;
        spec.support = support.name;
        spec.supportState = support.stateId;
        found = true;
        break;
      }
      if (!found) fail("no_support");
    }
    if (tool === "body_interact") {
      if (
        /chest|barrel|furnace|hopper|dispenser|dropper|shulker|brewing|crafter/.test(
          spec.block,
        )
      )
        fail("containers_disabled");
      if (!/door|button|lever|_bed$|respawn_anchor/.test(spec.block))
        fail("unsupported_target");
      if (spec.offHand) fail("empty_offhand_required");
    }
    if (tool === "body_dig") {
      for (const v of Object.values(faces)) {
        const n = this.block(spec.target.map((x, i) => x + v[i]));
        if (/water|lava|sand|gravel|anvil|redstone/.test(n.name))
          spec.hazards.push(n.name);
      }
      if (["air", "cave_air", "void_air", "water", "lava"].includes(spec.block))
        fail("unsupported_target");
    }
    if (tool === "body_use_item") {
      const item = this.item(args.item);
      args.item = item.name;
      spec.slot = item.slot;
      spec.count = item.count;
      if (!safeFoods.has(item.name)) {
        if (
          ![
            "water_bucket",
            "lava_bucket",
            "flint_and_steel",
            "fire_charge",
          ].includes(item.name)
        )
          fail("unsupported_item");
        const cursor = b.blockAtCursor(5);
        if (!cursor) fail("target_not_loaded");
        spec.target = pos(cursor.position);
        spec.block = cursor.name;
        spec.blockState = cursor.stateId;
        spec.look = [b.entity.yaw, b.entity.pitch];
      }
    }
    if (tool === "body_command") {
      if (typeof args.command !== "string") fail("invalid_args");
      args.command = args.command.trim();
    }
    checkSpec(spec, this.policy);
    return spec;
  }
  inspect(p) {
    try {
      this.envelope(p);
      if (this.paused || this.policy.mode !== "assist") fail("body_paused");
      if (this.active) fail("busy");
      const spec = this.describe(p.tool, p.args);
      if (!Number.isFinite(p.expiresAt) || p.expiresAt <= Date.now())
        fail("stale");
      spec.expiresAt = Math.min(spec.expiresAt, p.expiresAt);
      for (const [id, x] of this.prepared)
        if (x.expiresAt < Date.now()) this.prepared.delete(id);
      if (this.prepared.size >= 32) fail("busy");
      this.prepared.set(spec.id, freeze(spec));
      this.send("body.inspect_result", {
        connectionId: this.connectionId,
        inspectId: p.inspectId,
        ok: true,
        spec,
      });
    } catch (e) {
      this.send("body.inspect_result", {
        connectionId: this.connectionId,
        inspectId: p.inspectId,
        ok: false,
        code: e.code || "failed",
        error: clean(e.message),
      });
    }
  }
  ensure(a) {
    if (
      a.abort.signal.aborted ||
      this.epoch !== a.epoch ||
      !this.ready ||
      !this.connected ||
      performance.now() - this.lastLease > 1500
    )
      fail(a.reason || "canceled");
    if (!readTools.has(a.tool)) {
      if (this.paused || this.policy.mode !== "assist") fail("body_paused");
    }
  }
  async step(a, fn) {
    this.ensure(a);
    const result = await fn();
    this.ensure(a);
    return result;
  }
  limit(key, n) {
    const now = performance.now();
    const recent = (this.limits.get(key) || []).filter((t) => now - t < 60000);
    if (recent.length >= n) fail("rate_limited");
    recent.push(now);
    this.limits.set(key, recent);
  }
  async call(p) {
    if (this.results.has(p.callId)) {
      this.send("body.result", this.results.get(p.callId));
      return;
    }
    if (this.active?.callId === p.callId) return;
    let a, timeout, resolveDone;
    const result = {
      callId: p.callId,
      connectionId: this.connectionId,
      controlEpoch: this.epoch,
      ok: false,
      outcome: "not_executed",
      code: "",
      error: "",
      summary: "",
      elapsedMs: 0,
    };
    try {
      this.envelope(p);
      if (this.active) fail("busy");
      if (
        !capabilities.includes(
          {
            body_observe: "observe",
            body_inventory: "observe",
            body_find_blocks: "observe",
            body_goto: "move",
            body_look_at: "move",
            body_dig: "dig",
            body_place: "place",
            body_interact: "interact",
            body_use_item: "item",
            body_say: "chat",
            body_command: "command",
          }[p.tool],
        )
      )
        fail("unsupported");
      if (
        !Number.isInteger(p.timeoutMs) ||
        p.timeoutMs < 1 ||
        p.timeoutMs > 20000
      )
        fail("invalid_args");
      this.limit("all", 120);
      if (!readTools.has(p.tool)) {
        this.limit(
          "actions",
          Math.min(120, this.policy.limits?.actionsPerMin || 60),
        );
        const cat =
          p.tool === "body_say"
            ? "chat"
            : p.tool === "body_command"
              ? "commands"
              : [
                    "body_dig",
                    "body_place",
                    "body_interact",
                    "body_use_item",
                  ].includes(p.tool)
                ? "blocks"
                : null;
        if (cat)
          this.limit(
            cat,
            Math.min(120, this.policy.limits?.[cat + "PerMin"] || 30),
          );
      }
      a = {
        callId: p.callId,
        tool: p.tool,
        epoch: this.epoch,
        startedAt: Date.now(),
        abort: new AbortController(),
        reason: "canceled",
        submitted: false,
        done: new Promise((r) => {
          resolveDone = r;
        }),
      };
      if (!readTools.has(p.tool)) {
        if (this.paused || this.policy.mode !== "assist") fail("body_paused");
        const spec = this.prepared.get(p.specId);
        if (
          !spec ||
          spec.expiresAt < Date.now() ||
          spec.controlEpoch !== this.epoch ||
          spec.policyRevision !== this.revision ||
          spec.tool !== p.tool
        )
          fail("stale");
        const fresh = this.describe(p.tool, spec.args);
        for (const key of [
          "worldId",
          "dimension",
          "block",
          "blockState",
          "support",
          "supportState",
          "mainHand",
          "offHand",
          "slot",
          "count",
          "target",
          "reference",
          "faceVector",
          "look",
          "hazards",
        ])
          if (JSON.stringify(fresh[key]) !== JSON.stringify(spec[key]))
            fail("stale");
        if (distance(fresh.pos, spec.pos) > 0.75) fail("stale");
        checkGrant(spec, p.approvalGrant, this.policy, this.epoch);
        if (risk(spec, this.policy)) {
          if (this.usedGrants.has(p.approvalGrant.approvalId)) fail("stale");
          this.usedGrants.add(p.approvalGrant.approvalId);
          if (this.usedGrants.size > 256)
            this.usedGrants.delete(this.usedGrants.values().next().value);
        }
        this.prepared.delete(spec.id);
        a.spec = spec;
      }
      this.active = a;
      timeout = setTimeout(() => {
        a.reason = "timeout";
        a.abort.abort();
        this.stopInputs();
      }, p.timeoutMs);
      this.publishState();
      const abort = new Promise((_, reject) =>
        a.abort.signal.addEventListener(
          "abort",
          () => reject(Object.assign(Error(a.reason), { code: a.reason })),
          { once: true },
        ),
      );
      result.data = await Promise.race([
        this.perform(a, this.args(p.args)),
        abort,
      ]);
      this.ensure(a);
      result.ok = true;
      result.outcome = ["body_say", "body_command"].includes(p.tool)
        ? "submitted"
        : "confirmed";
      result.summary =
        result.outcome === "submitted"
          ? "已提交，实际结果以服务器状态为准"
          : "动作完成，已读取结果状态";
    } catch (e) {
      result.code = e.code || "failed";
      result.error = clean(e.message);
      result.summary = result.error;
      result.outcome = a?.submitted ? "unknown" : "not_executed";
      if (a?.tool === "body_goto" && this.connected && this.bot?.entity) {
        result.outcome = "confirmed";
        result.data = {
          partial: true,
          reached: false,
          position: pos(this.bot.entity.position),
        };
      } else if (a?.submitted) {
        this.paused = true;
        this.prepared.clear();
      }
    } finally {
      clearTimeout(timeout);
      if (a && this.active === a) {
        this.stopInputs();
        this.active = null;
      }
      if (resolveDone) resolveDone();
      result.elapsedMs = a ? Date.now() - a.startedAt : 0;
      result.state = this.state();
      this.results.set(p.callId, result);
      if (this.results.size > 128)
        this.results.delete(this.results.keys().next().value);
      this.send("body.result", result);
      this.publishState();
    }
  }
  async perform(a, raw) {
    const b = this.bot;
    if (!this.connected || !b) fail("body_offline");
    const t = a.tool,
      sp = a.spec,
      args = sp?.args || raw;
    if (t === "body_observe") {
      const range = Math.max(4, Math.min(16, args.range || 8));
      return {
        state: this.state(),
        entities: Object.values(b.entities)
          .filter(
            (e) =>
              e !== b.entity &&
              e.position.distanceTo(b.entity.position) <= range,
          )
          .slice(0, 16)
          .map((e) => ({
            id: e.id,
            type: e.name || e.type,
            player: clean(e.username).slice(0, 32),
            position: pos(e.position),
          })),
        look: b.blockAtCursor(5)?.name || null,
        events: this.recent.slice(-8).map((e) => ({
          type: e.type,
          at: e.at,
          data: e.data,
          untrusted: true,
        })),
      };
    }
    if (t === "body_inventory") {
      return {
        items: b.inventory
          .items()
          .filter((i) => !args.filter || i.name.includes(args.filter))
          .slice(0, 16)
          .map((i) => ({
            slot: i.slot,
            name: i.name,
            count: i.count,
            durability: i.durabilityUsed || 0,
          })),
        total: b.inventory.items().length,
      };
    }
    if (t === "body_find_blocks") {
      if (typeof args.blocks !== "string" || args.blocks.length > 500)
        fail("invalid_args");
      const ids = args.blocks
        .split(",")
        .map((x) => b.registry.blocksByName[nameOf(x.trim())]?.id)
        .filter((x) => x !== undefined);
      if (!ids.length) fail("unsupported_block");
      const found = b.findBlocks({
        matching: ids,
        maxDistance: Math.min(32, args.radius || 16),
        count: Math.min(16, args.count || 8),
      });
      return {
        blocks: found.map((p) => ({
          position: pos(p),
          name: b.blockAt(p)?.name,
        })),
      };
    }
    if (t === "body_goto") {
      this.movements();
      a.submitted = true;
      const range = args.range || 2;
      const goal = new pathfinder.goals.GoalNear(...sp.target, range);
      // Search nodes and optimized path nodes use corners and centers respectively.
      // Compare their physical standing point instead of shrinking the radius below
      // one block, which can make a sliced search's goal unreachable.
      goal.isEnd = (node) =>
        distance(
          [Math.floor(node.x) + 0.5, node.y, Math.floor(node.z) + 0.5],
          sp.target,
        ) <= range;
      await this.step(a, () => b.pathfinder.goto(goal));
      const final = pos(b.entity.position);
      const d = distance(final, sp.target);
      if (d > (args.range || 2) + 0.25) fail("path_not_found");
      return { reached: true, position: final, distance: d };
    }
    if (t === "body_look_at") {
      await this.step(a, () => b.lookAt(new Vec3(...sp.target), true));
      return { yaw: b.entity.yaw, pitch: b.entity.pitch };
    }
    if (t === "body_say") {
      this.ensure(a);
      a.submitted = true;
      b.chat(args.text);
      return { submitted: true };
    }
    if (t === "body_command") {
      const output = [];
      const listener = (text) => {
        if (output.length < 8) output.push(clean(text));
      };
      b.on("messagestr", listener);
      try {
        this.ensure(a);
        a.submitted = true;
        b.chat(
          args.command.startsWith("/") ? args.command : "/" + args.command,
        );
        await this.step(a, () => new Promise((r) => setTimeout(r, 1000)));
        return { submitted: true, output, untrusted: true };
      } finally {
        b.off("messagestr", listener);
      }
    }
    const verifyTarget = () => {
      this.ensure(a);
      if (this.world().worldId !== sp.worldId) fail("world_unknown");
      if (sp.target && this.block(sp.target).stateId !== sp.blockState)
        fail("stale");
      if (sp.reference && this.block(sp.reference).stateId !== sp.supportState)
        fail("stale");
      if ((b.inventory.slots[45]?.name || "") !== sp.offHand) fail("stale");
      if (t === "body_dig") {
        const hazards = Object.values(faces)
          .map((v) => this.block(sp.target.map((x, i) => x + v[i])).name)
          .filter((n) => /water|lava|sand|gravel|anvil|redstone/.test(n));
        if (JSON.stringify(hazards) !== JSON.stringify(sp.hazards))
          fail("stale");
      }
    };
    if (t === "body_dig") {
      const block = this.block(sp.target);
      const item = b.pathfinder.bestHarvestTool(block);
      if (item) await this.step(a, () => b.equip(item, "hand"));
      verifyTarget();
      if (!b.canDigBlock(block)) fail("not_in_reach");
      a.submitted = true;
      await this.step(a, () => b.dig(block));
      const after = this.block(sp.target);
      if (after.stateId === sp.blockState) fail("server_rejected");
      return { position: sp.target, before: sp.block, after: after.name };
    }
    if (t === "body_place") {
      await this.step(a, () => b.equip(this.item(args.item), "hand"));
      verifyTarget();
      if (b.heldItem?.name !== nameOf(args.item)) fail("stale");
      a.submitted = true;
      await this.step(a, () =>
        b.placeBlock(this.block(sp.reference), new Vec3(...sp.faceVector)),
      );
      const block = this.block(sp.target);
      if (block.name !== nameOf(args.item)) fail("server_rejected");
      return { position: sp.target, block: block.name };
    }
    if (t === "body_interact") {
      if (b.heldItem) await this.step(a, () => b.unequip("hand"));
      if (b.heldItem) fail("empty_hands_required");
      verifyTarget();
      a.submitted = true;
      await this.step(a, () => b.activateBlock(this.block(sp.target)));
      await this.step(a, () => new Promise((r) => setTimeout(r, 300)));
      const after = this.block(sp.target);
      if (after.stateId === sp.blockState) fail("unconfirmed_interaction");
      return {
        position: sp.target,
        before: sp.blockState,
        after: after.stateId,
      };
    }
    if (t === "body_use_item") {
      await this.step(a, () => b.equip(this.item(args.item), "hand"));
      verifyTarget();
      if (b.heldItem?.name !== nameOf(args.item)) fail("stale");
      a.submitted = true;
      if (safeFoods.has(nameOf(args.item))) {
        await this.step(a, () => b.consume());
        return {
          food: b.food,
          remaining: b.inventory
            .items()
            .filter((i) => i.name === nameOf(args.item))
            .reduce((n, i) => n + i.count, 0),
        };
      }
      await this.step(a, () => b.look(sp.look[0], sp.look[1], true));
      verifyTarget();
      if (["flint_and_steel", "fire_charge"].includes(nameOf(args.item)))
        await this.step(a, () => b.activateBlock(this.block(sp.target)));
      else b.activateItem();
      await this.step(
        a,
        () =>
          new Promise((r) => setTimeout(r, Math.max(500, args.holdMs || 0))),
      );
      b.deactivateItem();
      const after = b.inventory.slots[sp.slot];
      const neighbors = Object.values(faces).map(
        (v) => this.block(sp.target.map((x, i) => x + v[i])).name,
      );
      if (
        nameOf(args.item) === "water_bucket" &&
        after?.name === "bucket" &&
        neighbors.includes("water")
      )
        return { used: args.item, confirmed: true };
      if (
        nameOf(args.item) === "lava_bucket" &&
        after?.name === "bucket" &&
        neighbors.includes("lava")
      )
        return { used: args.item, confirmed: true };
      if (
        ["flint_and_steel", "fire_charge"].includes(nameOf(args.item)) &&
        neighbors.includes("fire")
      )
        return { used: args.item, confirmed: true };
      fail("outcome_unknown");
    }
    fail("unsupported");
  }
  async close() {
    this.ready = false;
    this.disconnected = true;
    clearInterval(this.safetyTimer);
    clearInterval(this.stateTimer);
    clearTimeout(this.reconnectTimer);
    this.pause("shutdown");
    this.bot?.quit();
  }
}
