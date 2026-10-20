import test from "node:test";
import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
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
test("player command transport keeps WorldEdit double slash and immutable preparation", async () => {
  const runtime = new MinecraftBody(
    { minecraft: { username: "MineBodyTest" }, backend: { bodyId: "main" } },
    () => {},
  );
  const sent = [];
  runtime.policy = policy;
  runtime.epoch = 1;
  runtime.ready = true;
  runtime.connected = true;
  runtime.paused = false;
  runtime.lastLease = performance.now();
  runtime.bot = Object.assign(new EventEmitter(), {
    entity: { position: { x: 0, y: 64, z: 0 } },
    game: { dimension: "overworld" },
    inventory: { slots: Array(46).fill(null) },
    chat(text) {
      sent.push(text);
    },
    quit() {},
    clearControlStates() {},
    deactivateItem() {},
    stopDigging() {},
    pathfinder: { setGoal() {} },
  });
  try {
    for (const [input, wire] of [
      ["help", "/help"],
      ["/give MineBodyTest stone 1", "/give MineBodyTest stone 1"],
      ["//pos1 1,64,1", "//pos1 1,64,1"],
    ]) {
      runtime.lastLease = performance.now();
      const prepared = runtime.describe("body_command", { command: input });
      assert.equal(
        runtime.describe("body_command", prepared.args).args.command,
        prepared.args.command,
      );
      const action = {
        tool: "body_command",
        epoch: 1,
        abort: new AbortController(),
        spec: prepared,
      };
      // Prepared args, not a later dispatch payload, determine the command.
      const result = await runtime.perform(action, { command: "op somebody" });
      assert.equal(result.submitted, true);
      assert.equal(sent.at(-1), wire);
      assert.equal(runtime.bot.listenerCount("messagestr"), 0);
    }
    for (const command of [
      "/agent test",
      "//ma test",
      "/minecraft:AI test",
      "mineagent:/agent test",
      "/",
      "//",
    ]) {
      assert.throws(() => runtime.describe("body_command", { command }));
    }
  } finally {
    await runtime.close();
  }
});
test("unrestricted zones allow distant positions but keep validation and hard denies", () => {
  const unrestricted = { ...policy, zones: { mode: "unrestricted" } };
  assert.equal(inside(unrestricted, "yard", [100000, 64, -100000]), true);
  assert.equal(inside(unrestricted, "", [0, 64, 0]), false);
  assert.equal(inside(unrestricted, "yard", [Infinity, 64, 0]), false);
  assert.equal(
    inside({ ...policy, zones: { mode: "typo" } }, "yard", [0, 64, 0]),
    false,
  );
  assert.throws(() =>
    checkSpec({ ...spec, block: "command_block" }, unrestricted),
  );
});
test("edit zones never fence movement, looking, chat or commands", () => {
  for (const tool of [
    "body_goto",
    "body_look_at",
    "body_say",
    "body_command",
  ]) {
    const args =
      tool === "body_command"
        ? { command: "//size" }
        : tool === "body_say"
          ? { text: "hello" }
          : {};
    assert.doesNotThrow(() =>
      checkSpec(
        {
          ...spec,
          tool,
          args,
          pos: [100, 64, 100],
          target: [101, 64, 100],
          mainHand: "",
        },
        policy,
      ),
    );
  }
  for (const tool of ["body_dig", "body_place"]) {
    assert.throws(
      () =>
        checkSpec(
          { ...spec, tool, pos: [100, 64, 100], target: [101, 64, 100] },
          policy,
        ),
      /out_of_zone/,
    );
    assert.doesNotThrow(() =>
      checkSpec(
        { ...spec, tool, pos: [31, 64, 0], target: [30, 64, 0] },
        policy,
      ),
    );
  }
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
test("movement outside edit zones keeps its lease and pause checks", async () => {
  const runtime = new MinecraftBody(
    { minecraft: { username: "MineBodyTest" }, backend: { bodyId: "main" } },
    () => {},
  );
  runtime.policy = policy;
  runtime.ready = runtime.connected = true;
  runtime.paused = false;
  runtime.epoch = 1;
  runtime.lastLease = performance.now();
  runtime.bot = {
    entity: { position: { x: 100, y: 200, z: 100 } },
    game: { dimension: "overworld" },
    quit() {},
  };
  const action = { tool: "body_goto", epoch: 1, abort: new AbortController() };
  assert.doesNotThrow(() => runtime.ensure(action));
  runtime.paused = true;
  assert.throws(() => runtime.ensure(action), /body_paused/);
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

test("navigation failures preserve concrete cause, partial progress and listener cleanup", async () => {
  for (const [name, code] of [
    ["NoPath", "path_not_found"],
    ["Timeout", "path_search_timeout"],
    ["GoalChanged", "goal_changed"],
    ["PathStopped", "path_stopped"],
  ]) {
    const sent = [];
    const runtime = new MinecraftBody(
      { minecraft: { username: "MineBodyTest" }, backend: { bodyId: "main" } },
      (type, data) => sent.push({ type, data }),
    );
    Object.assign(runtime, {
      policy,
      connectionId: "fixture",
      epoch: 1,
      revision: 1,
      ready: true,
      connected: true,
      paused: false,
      lastLease: performance.now(),
    });
    runtime.movements = () => {};
    const bot = Object.assign(new EventEmitter(), {
      entity: { position: { x: 0, y: 64, z: 0 } },
      game: { dimension: "overworld" },
      inventory: { slots: Array(46).fill(null) },
      blockAt(p) {
        return { name: "air", stateId: 0, position: p };
      },
      quit() {},
      clearControlStates() {},
      deactivateItem() {},
      stopDigging() {},
    });
    bot.pathfinder = {
      setGoal() {},
      async goto() {
        bot.emit("path_update", {
          status: name === "Timeout" ? "timeout" : "noPath",
          visitedNodes: 42,
          generatedNodes: 50,
          time: 20,
        });
        bot.entity.position.x = 2;
        const e = new Error("deterministic route failure");
        e.name = name;
        throw e;
      },
    };
    runtime.bot = bot;
    try {
      const spec = runtime.describe("body_goto", {
        x: 5,
        y: 64,
        z: 0,
        range: 1,
      });
      runtime.prepared.set(spec.id, spec);
      await runtime.call({
        callId: "nav-" + name,
        connectionId: "fixture",
        controlEpoch: 1,
        policyRevision: 1,
        tool: "body_goto",
        args: spec.args,
        specId: spec.id,
        timeoutMs: 1000,
      });
      const r = sent.findLast((e) => e.type === "body.result").data;
      assert.equal(r.ok, false);
      assert.equal(r.code, code);
      assert.equal(r.outcome, "confirmed");
      assert.equal(r.data.reached, false);
      assert.equal(r.data.progressDistance, 2);
      assert.equal(r.data.distance, 3);
      assert.equal(r.data.path.visitedNodes, 42);
      assert.equal(bot.listenerCount("path_update"), 0);
      assert.equal(runtime.lastNavigation.code, code);
    } finally {
      await runtime.close();
    }
  }
});
