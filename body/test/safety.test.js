import test from "node:test";
import assert from "node:assert/strict";
import { checkSpec, checkGrant, risk, inside } from "../src/policy.js";
import { MinecraftBody } from "../src/minecraft.js";
const policy = {
  connectionId: "fixture",
  controlEpoch: 1,
  policyRevision: 1,
  bodyId: "main",
  name: "MineBodyTest",
  mode: "assist",
  expectedMcVersion: "26.2",
  zones: {
    mode: "allowlist",
    areas: [{ worldId: "yard", min: [-10, -64, -10], max: [30, 100, 30] }],
  },
  worldMappings: [{ worldId: "yard", dimension: "minecraft:overworld" }],
};
const spec = {
  id: "prepared-fixture",
  tool: "body_interact",
  args: {},
  worldId: "yard",
  dimension: "minecraft:overworld",
  pos: [0, 64, 0],
  target: [1, 64, 1],
  block: "oak_door",
  mainHand: "lava_bucket",
  offHand: "",
  hazards: [],
};
test("unrestricted zones allow distant positions but keep validation and hard denies", () => {
  const unrestricted = { ...policy, zones: { mode: "unrestricted" } };
  assert.equal(inside(unrestricted, "yard", [100000, 64, -100000]), true);
  assert.equal(inside(unrestricted, "", [0, 64, 0]), false);
  assert.equal(inside(unrestricted, "yard", [Infinity, 64, 0]), false);
  assert.equal(inside({ ...policy, zones: { mode: "typo" } }, "yard", [0, 64, 0]), false);
  assert.throws(() => checkSpec({ ...spec, block: "command_block" }, unrestricted));
});
test("implicit hand and contextual beds require a matching one-use grant", () => {
  assert.equal(risk(spec, policy), true);
  assert.throws(() => checkGrant(spec, null, policy, 1));
  assert.throws(() =>
    checkGrant(
      spec,
      {
        approvalId: "a",
        specId: spec.id,
        controlEpoch: 2,
        expiresAt: Date.now() + 1000,
      },
      policy,
      1,
    ),
  );
  assert.equal(
    risk(
      {
        ...spec,
        mainHand: "",
        block: "red_bed",
        dimension: "minecraft:the_nether",
      },
      policy,
    ),
    true,
  );
});
test("hard deny and say cannot bypass the action policy", () => {
  assert.throws(() => checkSpec({ ...spec, block: "command_block" }, policy));
  assert.throws(() =>
    checkSpec(
      { ...spec, tool: "body_say", args: { text: "/op other" } },
      policy,
    ),
  );
  assert.throws(() =>
    checkSpec(
      { ...spec, tool: "body_command", args: { command: "minecraft:ma stop" } },
      policy,
    ),
  );
  assert.equal(inside(policy, "other", [0, 64, 0]), false);
});
test("cancelled delayed operation cannot execute the next action", async () => {
  const runtime = new MinecraftBody(
    { minecraft: { username: "MineBodyTest" }, backend: { bodyId: "main" } },
    () => {},
  );
  let release,
    acted = false;
  runtime.ready = true;
  runtime.connected = true;
  runtime.epoch = 1;
  runtime.lastLease = performance.now();
  runtime.policy = policy;
  runtime.paused = false;
  runtime.bot = {
    entity: { position: { x: 0, y: 64, z: 0 } },
    game: { dimension: "overworld" },
    pathfinder: { setGoal() {} },
    stopDigging() {},
    clearControlStates() {},
    deactivateItem() {},
    quit() {},
  };
  const a = { abort: new AbortController(), epoch: 1, tool: "body_goto" };
  runtime.active = a;
  const chain = (async () => {
    await runtime.step(
      a,
      () =>
        new Promise((r) => {
          release = r;
        }),
    );
    acted = true;
  })();
  runtime.pause("control");
  release();
  await assert.rejects(chain);
  assert.equal(acted, false);
  await runtime.close();
});
test("stale connection cannot refresh the local lease or resume actions", async () => {
  const runtime = new MinecraftBody(
    { minecraft: { username: "MineBodyTest" }, backend: { bodyId: "main" } },
    () => {},
  );
  runtime.ready = true;
  runtime.policy = policy;
  runtime.connectionId = "new";
  runtime.epoch = 2;
  runtime.lastLease = 0;
  runtime.lease({ connectionId: "old", controlEpoch: 2 });
  assert.equal(runtime.lastLease, 0);
  assert.throws(() =>
    runtime.envelope({
      connectionId: "new",
      controlEpoch: 1,
      policyRevision: 1,
    }),
  );
  await runtime.close();
});
test("path constraints cover the entire body and both worlds", async () => {
  const runtime = new MinecraftBody(
    { minecraft: { username: "MineBodyTest" }, backend: { bodyId: "main" } },
    () => {},
  );
  runtime.policy = policy;
  assert.equal(runtime.inBounds("yard", [29.8, 64, 0]), false);
  assert.equal(runtime.inBounds("yard", [0, 99, 0]), false);
  assert.equal(runtime.inBounds("yard", [0, 64, 0]), true);
  assert.equal(runtime.inBounds("other", [0, 64, 0]), false);
  await runtime.close();
});

test("a newly introduced adjacent hazard invalidates a prepared ordinary dig", async () => {
  const sent = [];
  let hazard = false,
    dug = 0;
  const runtime = new MinecraftBody(
    {
      minecraft: {
        username: "MineBodyTest",
        version: "26.1",
        targetVersion: "26.2",
      },
      backend: { bodyId: "main" },
    },
    (type, data) => sent.push({ type, data }),
  );
  runtime.policy = policy;
  runtime.connectionId = "fixture";
  runtime.epoch = 1;
  runtime.revision = 1;
  runtime.ready = true;
  runtime.connected = true;
  runtime.paused = false;
  runtime.lastLease = performance.now();
  const inventory = { slots: Array(46).fill(null), items: () => [] };
  runtime.bot = {
    entity: { position: { x: 0, y: 64, z: 0 }, yaw: 0, pitch: 0 },
    game: { dimension: "overworld", gameMode: "survival" },
    inventory,
    pathfinder: { setGoal() {} },
    stopDigging() {},
    clearControlStates() {},
    deactivateItem() {},
    quit() {},
    dig() {
      dug++;
    },
    blockAt(p) {
      const target = p.x === 1 && p.y === 64 && p.z === 0;
      const dangerous = hazard && p.x === 2 && p.y === 64 && p.z === 0;
      return {
        name: target ? "oak_log" : dangerous ? "lava" : "air",
        stateId: target ? 10 : dangerous ? 20 : 0,
        position: p,
        boundingBox: target ? "block" : "empty",
      };
    },
  };
  runtime.inspect({
    inspectId: "inspect",
    connectionId: "fixture",
    controlEpoch: 1,
    policyRevision: 1,
    tool: "body_dig",
    args: { x: 1, y: 64, z: 0 },
    expiresAt: Date.now() + 30000,
  });
  const prepared = sent.find((x) => x.type === "body.inspect_result");
  assert.equal(prepared.data.ok, true);
  hazard = true;
  await runtime.call({
    callId: "call",
    connectionId: "fixture",
    controlEpoch: 1,
    policyRevision: 1,
    tool: "body_dig",
    args: { x: 1, y: 64, z: 0 },
    specId: prepared.data.spec.id,
    timeoutMs: 1000,
  });
  const result = sent.find((x) => x.type === "body.result").data;
  assert.equal(result.code, "stale");
  assert.equal(result.outcome, "not_executed");
  assert.equal(dug, 0);
  await runtime.close();
});

test("missing control leases stop active input without a backend round trip", async () => {
  const runtime = new MinecraftBody(
    { minecraft: { username: "MineBodyTest" }, backend: { bodyId: "main" } },
    () => {},
  );
  let stopped = 0;
  runtime.ready = true;
  runtime.connected = true;
  runtime.paused = false;
  runtime.lastLease = performance.now() - 1600;
  runtime.bot = {
    entity: { position: { x: 0, y: 64, z: 0 } },
    game: { dimension: "overworld" },
    pathfinder: {
      setGoal() {
        stopped++;
      },
    },
    stopDigging() {},
    clearControlStates() {},
    deactivateItem() {},
    quit() {},
  };
  try {
    await new Promise((r) => setTimeout(r, 150));
    assert.equal(runtime.paused, true);
    assert.ok(stopped > 0);
  } finally {
    await runtime.close();
  }
});

test("goto uses the same physical radius for integer and optimized path nodes", async () => {
  const runtime = new MinecraftBody(
    { minecraft: { username: "MineBodyTest" }, backend: { bodyId: "main" } },
    () => {},
  );
  runtime.ready = true;
  runtime.connected = true;
  runtime.paused = false;
  runtime.epoch = 1;
  runtime.lastLease = performance.now();
  runtime.policy = policy;
  runtime.movements = () => {};
  runtime.bot = {
    entity: { position: { x: 0.5, y: 64, z: 0.5 } },
    game: { dimension: "overworld" },
    quit() {},
    clearControlStates() {},
    deactivateItem() {},
    stopDigging() {},
    pathfinder: {
      setGoal() {},
      async goto(goal) {
        assert.equal(goal.isEnd({ x: 0, y: 64, z: 0 }), true);
        assert.equal(goal.isEnd({ x: 0.5, y: 64, z: 0.5 }), true);
        assert.equal(goal.isEnd({ x: 1, y: 64, z: 1 }), false);
      },
    },
  };
  const action = {
    tool: "body_goto",
    epoch: 1,
    abort: new AbortController(),
    spec: { target: [0, 64, 0] },
  };
  try {
    const result = await runtime.perform(action, { range: 1 });
    assert.equal(result.reached, true);
    assert.ok(result.distance <= 1);
  } finally {
    await runtime.close();
  }
});
