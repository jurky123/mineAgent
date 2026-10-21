import test from "node:test";
import assert from "node:assert/strict";
import { Vec3 } from "vec3";
import { resolveDestination } from "../src/navigation.js";
import { MinecraftBody } from "../src/minecraft.js";
const fixture = (map = {}, floor = 63, y = 64) => ({
  entity: { position: new Vec3(0.5, y, 0.5) },
  blockAt(p) {
    const name =
      map[`${p.x},${p.y},${p.z}`] || (p.y === floor ? "stone" : "air");
    return {
      name,
      position: p,
      boundingBox: ["air", "water"].includes(name) ? "empty" : "block",
      shapes: ["air", "water"].includes(name) ? [] : [[0, 0, 0, 1, 1, 1]],
    };
  },
});
test("arrival region accepts a wall with an accessible neighbor, without changing explicit height", () => {
  const b = fixture({ "2,64,0": "stone", "2,65,0": "stone" });
  const r = resolveDestination(b, { x: 2, y: 64, z: 0, range: 2 });
  assert.deepEqual(r.target, [2, 64, 0]);
  assert.ok(r.candidates.length > 0);
  assert.ok(
    r.candidates.every((p) => Math.hypot(p[0] - 2, p[1] - 64, p[2]) <= 2),
  );
  const walls = {};
  for (let x = 1; x <= 3; x++)
    for (let z = -1; z <= 1; z++)
      for (let y = 64; y <= 65; y++) walls[`${x},${y},${z}`] = "stone";
  assert.throws(
    () =>
      resolveDestination(fixture(walls), { x: 2.5, y: 64, z: 0.5, range: 1 }),
    (e) => e.code === "target_below_ground",
  );
});
test("omitted height resolves loaded floors over eight blocks down but explicit air goals do not silently drop", () => {
  const b = fixture({}, 62, 75);
  assert.equal(resolveDestination(b, { x: 0, z: 0 }).target[1], 63);
  assert.throws(
    () => resolveDestination(b, { x: 0, y: 75, z: 0 }),
    (e) => e.code === "target_above_ground" && e.data.suggestedY === 63,
  );
});
test("water requires explicit surface swim; submerged goals and unknown sections fail", () => {
  const b = fixture({ "0,62,0": "water" }, -64, 75);
  assert.throws(
    () => resolveDestination(b, { x: 0.5, z: 0.5, range: 1 }),
    (e) => e.code === "water_only",
  );
  assert.equal(
    resolveDestination(b, { x: 0.5, z: 0.5, range: 1, movement: "swim" })
      .target[1],
    62,
  );
  assert.throws(
    () =>
      resolveDestination(b, {
        x: 0.5,
        y: 58,
        z: 0.5,
        range: 1,
        movement: "swim",
      }),
    (e) => e.code === "target_below_ground",
  );
  assert.throws(
    () =>
      resolveDestination(
        { entity: b.entity, blockAt: () => null },
        { x: 0, z: 0 },
      ),
    (e) => e.code === "terrain_unknown",
  );
  assert.throws(
    () => resolveDestination(b, { x: 0, z: 0, movement: "fly" }),
    (e) => e.code === "unsupported_movement",
  );
});
test("surface route crosses a two-deep pool and returns to shore, without diving or editing", async () => {
  const { createRequire } = await import("node:module");
  const req = createRequire(import.meta.url);
  const registry = req("minecraft-data")("26.1");
  const Block = req("prismarine-block")(registry);
  const AStar = req("mineflayer-pathfinder/lib/astar");
  const Move = req("mineflayer-pathfinder/lib/move");
  const { GoalBlock } = req("mineflayer-pathfinder").goals;
  const runtime = new MinecraftBody({ minecraft: {}, backend: {} }, () => {});
  let movements;
  runtime.bot = {
    registry,
    entities: {},
    game: { minY: -64 },
    entity: { position: new Vec3(0.5, -60, 0.5), effects: {} },
    inventory: { items: () => [] },
    blockAt(p) {
      if (p.x < -10 || p.x > 32 || p.z < -10 || p.z > 32) return null;
      const pool = p.x >= 10 && p.x <= 18 && p.z >= 6 && p.z <= 14;
      const name =
        pool && p.y >= -61 && p.y <= -60
          ? "water"
          : p.y <= -61
            ? "stone"
            : "air";
      const b = Block.fromProperties(registry.blocksByName[name].id, {}, 0);
      b.position = p.floored();
      return b;
    },
    pathfinder: {
      setMovements: (m) => {
        movements = m;
      },
      bestHarvestTool: () => null,
    },
  };
  const route = (from, to) => {
    const search = new AStar(
      new Move(...from, 0, 0),
      movements,
      new GoalBlock(...to),
      1000,
      10,
      64,
    );
    let r;
    do {
      r = search.compute();
    } while (r.status === "partial");
    return r;
  };
  try {
    runtime.movements("ground");
    assert.equal(movements.infiniteLiquidDropdownDistance, false);
    assert.equal(route([0, -60, 0], [14, -60, 10]).status, "noPath");
    runtime.movements("swim");
    for (const [from, to] of [
      [
        [0, -60, 0],
        [14, -60, 10],
      ],
      [
        [14, -60, 10],
        [0, -60, 0],
      ],
    ]) {
      const r = route(from, to);
      assert.equal(r.status, "success");
      assert.ok(
        r.path.every(
          (p) => p.y >= -60 && p.toBreak.length === 0 && p.toPlace.length === 0,
        ),
      );
    }
    assert.ok(
      movements
        .getNeighbors(new Move(14, -61, 10, 0, 0))
        .every((p) => p.y >= -60),
    );
    assert.ok(
      movements
        .getNeighbors(new Move(14, -50, 10, 0, 0))
        .every((p) => -50 - p.y <= 3),
      "vertical water drops retain the same three-block bound",
    );
    runtime.bot.entity.position = new Vec3(13.5, -60.04, 9.5);
    assert.equal(
      route([13, -61, 9], [0, -60, 0]).status,
      "success",
      "surface swimmer can leave water despite fractional feet below surface Y",
    );
    runtime.bot.entity.position = new Vec3(13.5, -60.6, 9.5);
    assert.equal(
      route([13, -61, 9], [0, -60, 0]).status,
      "noPath",
      "normalization does not enable genuinely submerged routes",
    );
    const poolBlocks = runtime.bot.blockAt;
    runtime.bot.blockAt = (p) => {
      let kind,
        properties = {};
      if (p.x >= 13 && p.x <= 15 && p.z === 9 && p.y === -60) {
        kind = "oak_slab";
        properties = { type: "bottom", waterlogged: true };
      } else if (p.x >= 13 && p.x <= 15 && p.z === 10 && p.y === -60)
        kind = "oak_planks";
      else if (p.x >= 13 && p.x <= 15 && p.z === 11 && p.y === -59) {
        kind = "oak_slab";
        properties = { type: "bottom", waterlogged: false };
      } else if (
        p.x >= 12 &&
        p.x <= 16 &&
        p.z >= 12 &&
        p.z <= 15 &&
        p.y === -59
      )
        kind = "oak_planks";
      if (!kind) return poolBlocks(p);
      const b = Block.fromProperties(
        registry.blocksByName[kind].id,
        properties,
        0,
      );
      b.position = p.floored();
      return b;
    };
    runtime.bot.entity.position = new Vec3(14.5, -59.465, 9.697);
    const { surfaceAbove, wetFootSupport } = await import(
      "../src/navigation.js"
    );
    assert.equal(wetFootSupport(runtime.bot), true);
    assert.equal(surfaceAbove(runtime.bot), -60);
    runtime.movements("swim");
    assert.equal(
      route([14, -60, 9], [14, -58, 14]).status,
      "success",
      "wet half-step supports restart with onGround=false",
    );
    runtime.bot.entity.position = new Vec3(4.5, 62, 0.5);
    runtime.bot.blockAt = (p) => {
      const name =
        p.x <= 0
          ? p.y <= 61
            ? "stone"
            : "air"
          : p.y >= 56 && p.y <= 62
            ? "water"
            : p.y <= 55
              ? "stone"
              : "air";
      const block = Block.fromProperties(registry.blocksByName[name].id, {}, 0);
      block.position = p.floored();
      return block;
    };
    runtime.movements("swim");
    assert.equal(movements.liquidCost, 0);
    const openWater = route([4, 62, 0], [4, 62, 24]);
    assert.equal(openWater.status, "success");
    assert.ok(
      openWater.path.every((p) => p.x > 0),
      "swimmer keeps the direct open-water route",
    );
    movements.liquidCost = 1;
    const bankBiased = route([4, 62, 0], [4, 62, 24]);
    assert.ok(
      bankBiased.path.some((p) => p.x <= 0),
      "old water penalty reproduces the bank detour",
    );
  } finally {
    clearInterval(runtime.safetyTimer);
    clearInterval(runtime.stateTimer);
  }
});

test("recovery scans only loaded open water, rejecting roofs and excessive depth", async () => {
  const { surfaceAbove } = await import("../src/navigation.js");
  const water = {};
  for (let y = 56; y <= 62; y++) water[`0,${y},0`] = "water";
  const bot = fixture(water, 55, 56);
  assert.equal(surfaceAbove(bot), 62);
  water["0,60,0"] = "stone";
  assert.throws(
    () => surfaceAbove(bot),
    (e) => e.code === "surface_recovery_blocked",
  );
  delete water["0,60,0"];
  assert.throws(
    () => surfaceAbove(bot, 4),
    (e) => e.code === "surface_recovery_blocked",
  );
  assert.throws(
    () => surfaceAbove({ entity: bot.entity, blockAt: () => null }),
    (e) => e.code === "surface_recovery_blocked",
  );
});

test("surfacing is action scoped, cancellable, and leaves no idle input timer", async () => {
  const water = {};
  for (let y = 56; y <= 62; y++) water[`0,${y},0`] = "water";
  const runtime = new MinecraftBody({ minecraft: {}, backend: {} }, () => {});
  const controls = [];
  runtime.bot = fixture(water, 55, 56);
  runtime.bot.entity.isInWater = true;
  runtime.bot.setControlState = (key, value) => controls.push([key, value]);
  runtime.ensure = (a) => {
    if (a.abort.signal.aborted)
      throw Object.assign(Error(a.reason), { code: a.reason });
  };
  try {
    const a = { abort: new AbortController(), reason: "action_canceled" };
    runtime.active = a;
    const pending = runtime.surfaceForNavigation(a);
    assert.deepEqual(controls.at(-1), ["jump", true]);
    a.abort.abort();
    await assert.rejects(pending, (e) => e.code === "action_canceled");
    assert.deepEqual(controls.at(-1), ["jump", false]);
    const count = controls.length;
    await new Promise((r) => setTimeout(r, 100));
    assert.equal(controls.length, count);
    const next = { abort: new AbortController() };
    runtime.active = next;
    const rise = runtime.surfaceForNavigation(next);
    setTimeout(() => {
      runtime.bot.entity.position.y = 62.6;
    }, 60);
    await rise;
    assert.equal(next.surfaceRecovery.fromY, 56);
    assert.equal(next.surfaceRecovery.surfaceY, 62);
    assert.deepEqual(controls.at(-1), ["jump", false]);
  } finally {
    clearInterval(runtime.safetyTimer);
    clearInterval(runtime.stateTimer);
  }
});

test("water contact recovery backs into the same known cell and is cancellable", async () => {
  const { contactBackoff } = await import("../src/navigation.js");
  const runtime = new MinecraftBody({ minecraft: {}, backend: {} }, () => {});
  const controls = [];
  let forward = false;
  runtime.bot = {
    entity: { position: new Vec3(14.5, -59.465, 9.697), isInWater: true },
    blockAt(p) {
      return p.z === 10
        ? { position: p, shapes: [[0, 0, 0, 1, 1, 1]] }
        : { position: p, shapes: [] };
    },
    async lookAt() {
      this.entity.yaw = 0;
    },
    setControlState(k, v) {
      controls.push([k, v]);
      if (k === "forward") forward = v;
    },
  };
  runtime.ensure = (a) => {
    if (a.abort.signal.aborted) throw Error("canceled");
  };
  assert.deepEqual(contactBackoff(runtime.bot).toArray(), [0, 0, -1]);
  let ticks;
  try {
    const a = { abort: new AbortController(), reason: "action_canceled" };
    runtime.active = a;
    const pending = runtime.backOffContact(a);
    await new Promise((r) => setTimeout(r, 10));
    a.abort.abort();
    await assert.rejects(pending);
    assert.equal(forward, false);
    const next = { abort: new AbortController() };
    runtime.active = next;
    ticks = setInterval(() => {
      if (forward && runtime.bot.entity.yaw === 0)
        runtime.bot.entity.position.z -= 0.08;
      runtime.bot.entity.yaw = 1; // a server correction can overwrite the look
    }, 20);
    await runtime.backOffContact(next);
    assert.ok(next.contactRecovery.distance >= 0.22);
    assert.equal(
      Math.floor(runtime.bot.entity.position.z),
      9,
      "backoff stays in original cell",
    );
    assert.equal(forward, false);
  } finally {
    clearInterval(ticks);
    clearInterval(runtime.safetyTimer);
    clearInterval(runtime.stateTimer);
  }
});

test("contact precision and backoff recognize torso walls, respect player height and reject unknown/overhead faces", async () => {
  const { contactPrecision, contactBackoff } = await import(
    "../src/navigation.js"
  );
  let wallY = -60,
    unknown = false;
  const bot = {
    entity: {
      position: new Vec3(12.6897504, -60.32227904479, 10.5),
      height: 1.8,
      isInWater: true,
    },
    controlState: { forward: false },
    blockAt(p) {
      if (unknown) return null;
      return {
        position: p,
        shapes: p.x === 13 && p.y === wallY ? [[0, 0, 0, 1, 1, 1]] : [],
      };
    },
  };
  const from = [12.7, -60.32227904479, 10.5],
    to = bot.entity.position.toArray();
  assert.equal(contactPrecision(bot, from, to), true);
  assert.deepEqual(contactBackoff(bot).toArray(), [-1, 0, 0]);
  wallY = -59;
  assert.equal(contactPrecision(bot, from, to), true);
  bot.entity.height = 0.6;
  assert.equal(contactPrecision(bot, from, to), false);
  bot.entity.height = 1.8;
  wallY = -58;
  assert.equal(contactPrecision(bot, from, to), false);
  wallY = -60;
  unknown = true;
  assert.equal(contactPrecision(bot, from, to), false);
  unknown = false;
  bot.controlState.forward = true;
  assert.equal(contactPrecision(bot, from, to), false);
});

test("precision gate reads Mineflayer non-enumerable control getters", async () => {
  const { contactPrecision } = await import("../src/navigation.js");
  let held = false;
  const controlState = {};
  Object.defineProperty(controlState, "forward", { get: () => held });
  assert.equal(Object.keys(controlState).length, 0);
  const bot = {
    entity: { height: 1.8 },
    controlState,
    blockAt(p) {
      return { position: p, shapes: [[0, 0, 0, 1, 1, 1]] };
    },
  };
  const from = [0.5, 62.535, 9.7],
    to = [0.5, 62.535, 9.697];
  assert.equal(contactPrecision(bot, from, to), true);
  held = true;
  assert.equal(contactPrecision(bot, from, to), false);
});

test("known horizontal contact tolerates millimetre water gravity but not vertical-only or larger idle moves", async () => {
  const { contactPrecision } = await import("../src/navigation.js");
  const bot = {
    entity: { height: 1.8, isInWater: true },
    controlState: {},
    blockAt(p) {
      return { position: p, shapes: [[0, 0, 0, 1, 1, 1]] };
    },
  };
  const from = [0.5, 61.0607543, 524.7],
    to = [0.5, 61.0657543, 524.6948546];
  assert.equal(contactPrecision(bot, from, to), true);
  bot.entity.isInWater = false;
  assert.equal(contactPrecision(bot, from, to), false);
  bot.entity.isInWater = true;
  assert.equal(
    contactPrecision(bot, from, [to[0], from[1] + 0.02, to[2]]),
    false,
  );
  assert.equal(
    contactPrecision(bot, from, [from[0], from[1] + 0.005, from[2]]),
    false,
  );
  assert.equal(
    contactPrecision(bot, from, [from[0] + 0.005, to[1], to[2]]),
    false,
  );
});

test("wet ladder recovery uses real open column geometry and refuses a panel intersecting the player", async () => {
  const { surfaceAbove } = await import("../src/navigation.js");
  let blocked = false;
  const bot = {
    entity: { position: new Vec3(12.4, 60.7, 7.45) },
    blockAt(p) {
      const name = p.y <= 60 ? "water" : p.y <= 62 ? "ladder" : "air";
      return {
        name,
        position: p,
        boundingBox: name === "ladder" ? "block" : "empty",
        shapes:
          name === "ladder" ? [[0, 0, blocked ? 0.5 : 0.8125, 1, 1, 1]] : [],
        getProperties: () => ({ waterlogged: name === "ladder" && p.y === 61 }),
      };
    },
  };
  assert.equal(surfaceAbove(bot), 61);
  blocked = true;
  assert.throws(() => surfaceAbove(bot), /surface_recovery_blocked|上浮/);
  blocked = false;
  bot.blockAt = () => null;
  assert.throws(() => surfaceAbove(bot), /surface_recovery_blocked|上浮/);
});

test("wet ladder ascent keeps native head clearance, forbids scaffolding and normalizes climb points", async () => {
  const { createRequire } = await import("node:module");
  const req = createRequire(import.meta.url);
  const registry = req("minecraft-data")("26.1"),
    Block = req("prismarine-block")(registry),
    AStar = req("mineflayer-pathfinder/lib/astar"),
    Move = req("mineflayer-pathfinder/lib/move");
  let ceiling = false,
    m;
  const source = [
    { x: 0.5, y: 64, z: 0.90625, hash: "0,63,0" },
    { x: 0.5, y: 64, z: 1.5, hash: "0,64,1" },
  ];
  const bot = {
    registry,
    entity: {
      position: new Vec3(0.4, 61.5, 0.45),
      effects: {},
      onGround: false,
    },
    entities: {},
    inventory: { items: () => [] },
    game: { minY: -64 },
    pathfinder: {
      bestHarvestTool: () => null,
      setMovements(v) {
        m = v;
      },
      *getPathFromTo() {
        yield { result: { path: source.map((n) => ({ ...n })) } };
      },
    },
    blockAt(p) {
      if (Math.abs(p.x) > 2 || Math.abs(p.z) > 2 || p.y < 58 || p.y > 66)
        return null;
      let name = p.y <= 62 ? "water" : "air",
        props = {};
      if (p.y === 58) name = "stone";
      if (p.x === 0 && p.z === 1 && p.y >= 61 && p.y <= 63) name = "oak_planks";
      if (p.x === 0 && p.z === 0 && p.y >= 61 && p.y <= 63) {
        name = "ladder";
        props = { facing: "north", waterlogged: p.y <= 62 };
      }
      if (ceiling && p.x === 0 && p.z === 0 && p.y === 63) name = "stone";
      const b = Block.fromProperties(registry.blocksByName[name].id, props, 0);
      b.position = p;
      return b;
    },
  };
  const runtime = new MinecraftBody({ minecraft: {}, backend: {} }, () => {});
  runtime.bot = bot;
  try {
    runtime.movements("ground");
    const up = [];
    m.getMoveUp(new Move(0, 61, 0, 0, 0), up);
    assert.ok(up.some((n) => n.y === 62));
    assert.ok(
      up.every((n) => n.toBreak.length === 0 && n.toPlace.length === 0),
    );
    const goal = {
      heuristic: (n) => Math.abs(n.y - 64) + Math.abs(n.z - 1) + Math.abs(n.x),
      isEnd: (n) => n.x === 0 && n.y === 64 && n.z === 1,
    };
    const search = new AStar(new Move(0, 61, 0, 0, 0), m, goal, 1000, 20, 8);
    let route;
    do {
      route = search.compute();
    } while (route.status === "partial");
    assert.equal(route.status, "success");
    const fixed = bot.pathfinder.getPathFromTo().next().value.result.path;
    assert.deepEqual([fixed[0].x, fixed[0].y, fixed[0].z], [0.5, 63, 0.5]);
    assert.deepEqual([fixed[1].x, fixed[1].y, fixed[1].z], [0.5, 64, 1.5]);
    assert.deepEqual(
      resolveDestination(bot, { x: 0.5, y: 64, z: 1.5, range: 1 }).candidates,
      [[0.5, 64, 1.5]],
      "the ladder panel is not a supporting floor at the search centre",
    );
    ceiling = true;
    const blocked = [];
    m.getMoveUp(new Move(0, 61, 0, 0, 0), blocked);
    assert.equal(
      blocked.length,
      0,
      "solid headroom remains forbidden without digging",
    );
  } finally {
    clearInterval(runtime.safetyTimer);
    clearInterval(runtime.stateTimer);
  }
});
