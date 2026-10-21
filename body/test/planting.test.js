import test from "node:test";
import assert from "node:assert/strict";
import { Vec3 } from "vec3";
import { MinecraftBody } from "../src/minecraft.js";
function fixture() {
  const sent = [];
  let soil = "farmland",
    crop = "air",
    placements = 0,
    refuse = false;
  const seed = { name: "wheat_seeds", slot: 36, count: 64 };
  const runtime = new MinecraftBody(
    {
      minecraft: { username: "Farmer", version: "26.1", targetVersion: "26.2" },
      backend: { bodyId: "main" },
    },
    (type, data) => sent.push({ type, data }),
  );
  Object.assign(runtime, {
    connectionId: "fixture",
    epoch: 1,
    revision: 1,
    ready: true,
    connected: true,
    paused: false,
    lastLease: performance.now(),
    policy: {
      mode: "assist",
      zones: {
        mode: "allowlist",
        areas: [{ worldId: "yard", min: [-10, -64, -10], max: [10, 100, 10] }],
      },
      worldMappings: [{ worldId: "yard", dimension: "minecraft:overworld" }],
    },
  });
  runtime.bot = {
    entity: { position: new Vec3(0.5, 64, 0.5) },
    game: { dimension: "overworld" },
    registry: { blocksByName: { wheat: {} } },
    heldItem: seed,
    inventory: { slots: Array(46).fill(null), items: () => [seed] },
    blockAt(p) {
      const name = p.y === 63 ? soil : crop;
      return {
        name,
        position: p,
        boundingBox: name === "air" ? "empty" : "block",
        stateId: name === "farmland" ? 5 : name === "air" ? 0 : 9,
      };
    },
    async equip(item) {
      this.heldItem = item;
    },
    async placeBlock() {
      placements++;
      if (!refuse) crop = "wheat";
    },
    pathfinder: { setGoal() {} },
    clearControlStates() {},
    deactivateItem() {},
    stopDigging() {},
    quit() {},
  };
  return {
    runtime,
    sent,
    seed,
    setSoil(v) {
      soil = v;
    },
    setRefuse(v) {
      refuse = v;
    },
    get placements() {
      return placements;
    },
  };
}
const args = { x: 1, y: 64, z: 1, item: "minecraft:wheat_seeds" };
test("wheat seed placement requires air above farmland and enforces crop/item deny rules", async () => {
  const f = fixture(),
    r = f.runtime;
  try {
    const spec = r.describe("body_place", args);
    assert.equal(spec.args.item, "wheat_seeds");
    assert.equal(spec.args.face, "up");
    assert.deepEqual(spec.reference, [1, 63, 1]);
    assert.throws(
      () => r.describe("body_place", { ...args, face: "north" }),
      /farmland_required/,
    );
    f.setSoil("dirt");
    assert.throws(() => r.describe("body_place", args), /farmland_required/);
    f.setSoil("farmland");
    for (const blocked of ["wheat", "minecraft:wheat", "wheat_seeds"]) {
      r.policy.denyBlocks = [blocked];
      assert.throws(() => r.describe("body_place", args), /denied_block/);
    }
    r.policy.denyBlocks = [];
    r.policy.denyItems = ["wheat_seeds"];
    assert.throws(() => r.describe("body_place", args), /denied_item/);
  } finally {
    await r.close();
  }
});
test("planting confirms the crop block rather than expecting a wheat_seeds block; refusals and soil changes do not count as success", async () => {
  for (const mode of ["plant", "refuse", "soil-changed"]) {
    const f = fixture(),
      r = f.runtime;
    try {
      const spec = r.describe("body_place", args);
      r.prepared.set(spec.id, spec);
      if (mode === "refuse") f.setRefuse(true);
      if (mode === "soil-changed") f.setSoil("dirt");
      await r.call({
        callId: mode,
        connectionId: "fixture",
        controlEpoch: 1,
        policyRevision: 1,
        tool: "body_place",
        args: spec.args,
        specId: spec.id,
        timeoutMs: 1000,
      });
      const result = f.sent.findLast((e) => e.type === "body.result").data;
      assert.equal(result.ok, mode === "plant");
      if (mode === "plant") {
        assert.equal(result.data.block, "wheat");
        assert.equal(result.data.item, "wheat_seeds");
      } else
        assert.equal(
          result.code,
          mode === "refuse" ? "server_rejected" : "farmland_required",
        );
      assert.equal(f.placements, mode === "soil-changed" ? 0 : 1);
    } finally {
      await r.close();
    }
  }
});
