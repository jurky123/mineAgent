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
      runtime.bot.entity.position.y = 62;
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
