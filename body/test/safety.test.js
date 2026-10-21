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
test("server position changes latch pause, record displacement and require explicit resume", async () => {
  for (const offset of [0, 0.5, 1.25, 50]) {
    const runtime = new MinecraftBody(
      { minecraft: { username: "MineBodyTest" }, backend: { bodyId: "main" } },
      () => {},
    );
    runtime.ready = true;
    runtime.connected = true;
    runtime.paused = false;
    runtime.connectionId = policy.connectionId;
    runtime.epoch = 1;
    runtime.onlineSince = Date.now() - 10000;
    runtime.lastLease = performance.now();
    runtime.policy = policy;
    runtime.bot = {
      entity: { position: { x: offset, y: 64, z: 0 } },
      pathfinder: { setGoal() {} },
      clearControlStates() {},
      stopDigging() {},
      deactivateItem() {},
      quit() {},
    };
    const a = { tool: "body_goto", abort: new AbortController() };
    runtime.active = a;
    runtime.prepared.set("old", spec);
    try {
      runtime.forcedMove({ position: [0, 64, 0], at: Date.now() });
      if (offset <= 0.75) {
        assert.equal(runtime.paused, false);
        assert.equal(a.abort.signal.aborted, false);
        assert.equal(runtime.recent.at(-1).data.correction, true);
        for (let i = 0; i < (offset === 0 ? 40 : 1); i++)
          runtime.forcedMove({ position: [0, 64, 0], at: Date.now() });
      }
      assert.equal(runtime.paused, true);
      assert.equal(runtime.state().pauseReason, "forced_move");
      assert.equal(a.abort.signal.aborted, true);
      assert.equal(a.reason, "forced_move");
      assert.equal(runtime.prepared.size, 0);
      const event = runtime.recent.find((e) => e.type === "forced_move");
      assert.deepEqual(event.data.from, [0, 64, 0]);
      assert.deepEqual(event.data.to, [offset, 64, 0]);
      assert.equal(event.data.distance, offset);
      assert.equal(event.data.tool, "body_goto");
      // Even a zero-distance server packet is not evidence of permission to
      // resume. The protocol cannot distinguish a small teleport from a nudge.
      runtime.lease({ connectionId: policy.connectionId, controlEpoch: 1 });
      assert.equal(runtime.paused, true);
      runtime.pause("lease_expired");
      assert.equal(runtime.state().pauseReason, "forced_move");
      runtime.active = null;
      await runtime.control({
        connectionId: policy.connectionId,
        controlEpoch: 2,
        action: "resume",
      });
      assert.equal(runtime.paused, false);
      assert.equal(runtime.state().pauseReason, "");
    } finally {
      await runtime.close();
    }
  }
});
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
    inventory: { slots: Array(46).fill(null) },
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
      {
        minecraft: {
          username: "MineBodyTest",
          viaProxy: true,
          targetVersion: "26.2",
        },
        backend: { bodyId: "main" },
      },
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
      physics: { playerHalfWidth: 0.3 },
      inventory: { slots: Array(46).fill(null) },
      blockAt(p) {
        return p.y === 63
          ? {
              name: "stone",
              stateId: 1,
              position: p,
              boundingBox: "block",
              shapes: [[0, 0, 0, 1, 1, 1]],
            }
          : {
              name: "air",
              stateId: 0,
              position: p,
              boundingBox: "empty",
              shapes: [],
            };
      },
      quit() {},
      clearControlStates() {},
      deactivateItem() {},
      stopDigging() {},
    });
    bot.pathfinder = {
      setGoal() {},
      async goto() {
        assert.equal(bot.physics.playerHalfWidth, 0.32);
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
        movement: "swim",
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
      assert.equal(
        bot.physics.playerHalfWidth,
        0.3,
        "collision margin restored after failed/canceled paths",
      );
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

test("small forced move without an active action still pauses", async () => {
  const runtime = new MinecraftBody(
    { minecraft: { username: "MineBodyTest" }, backend: { bodyId: "main" } },
    () => {},
  );
  runtime.paused = false;
  runtime.connected = true;
  runtime.onlineSince = Date.now() - 10000;
  runtime.bot = { entity: { position: { x: 0.25, y: 64, z: 0 } }, quit() {} };
  try {
    runtime.forcedMove({ position: [0, 64, 0] });
    assert.equal(runtime.paused, true);
  } finally {
    await runtime.close();
  }
});
test("late cancel is scoped to its action and preserves pause state", async () => {
  const sent = [];
  const r = new MinecraftBody(
    { minecraft: { username: "MineBodyTest" }, backend: { bodyId: "main" } },
    (t, d) => sent.push({ t, d }),
  );
  Object.assign(r, {
    ready: true,
    connectionId: "fixture",
    epoch: 1,
    paused: false,
  });
  const a = {
    callId: "new-action",
    abort: new AbortController(),
    done: Promise.resolve(),
  };
  r.active = a;
  try {
    await r.control({
      id: "late",
      action: "cancel",
      connectionId: "fixture",
      controlEpoch: 1,
      callId: "old-action",
    });
    assert.equal(a.abort.signal.aborted, false);
    assert.equal(r.paused, false);
    assert.equal(sent.at(-1).d.id, "late");
    r.active = null;
    await r.control({
      id: "idle",
      action: "cancel",
      connectionId: "fixture",
      controlEpoch: 1,
      callId: "",
    });
    assert.equal(r.paused, false);
    assert.equal(r.epoch, 1);
  } finally {
    await r.close();
  }
});

test("navigation rechecks hands for every implicit gate click and restores activation", async () => {
  const r = new MinecraftBody(
    { minecraft: { username: "MineBodyTest" }, backend: { bodyId: "main" } },
    () => {},
  );
  Object.assign(r, {
    ready: true,
    connected: true,
    paused: false,
    epoch: 1,
    lastLease: performance.now(),
    policy,
  });
  r.movements = () => {};
  let clicked = 0;
  const original = async () => {
    clicked++;
  };
  const b = {
    entity: { position: { x: 0.5, y: 64, z: 0.5 } },
    inventory: { slots: Array(46).fill(null) },
    activateBlock: original,
    game: { dimension: "overworld" },
    quit() {},
    clearControlStates() {},
    stopDigging() {},
    deactivateItem() {},
    pathfinder: {
      setGoal() {},
      async goto() {
        await assert.rejects(() => b.activateBlock({ name: "chest" }), {
          code: "unsafe_navigation_interaction",
        });
        b.inventory.slots[45] = { name: "lava_bucket" };
        await assert.rejects(
          () => b.activateBlock({ name: "oak_fence_gate" }),
          { code: "empty_hands_required" },
        );
        b.inventory.slots[45] = null;
        await b.activateBlock({ name: "oak_fence_gate" });
      },
    },
  };
  r.bot = b;
  const a = {
    tool: "body_goto",
    epoch: 1,
    abort: new AbortController(),
    spec: { target: [0, 64, 0] },
  };
  try {
    await r.perform(a, { range: 1 });
    assert.equal(clicked, 1);
    assert.equal(b.activateBlock, original);
  } finally {
    await r.close();
  }
});

test("millimetre collision corrections fit a bounded distance budget, large or cumulative moves still pause", () => {
  for (const steps of [
    [...Array(20).fill(0.003), 0.8],
    [0.3, 0.3, 0.3],
  ]) {
    const runtime = new MinecraftBody(
      { minecraft: { username: "MineBodyTest" }, backend: {} },
      () => {},
    );
    runtime.connected = true;
    runtime.ready = true;
    runtime.paused = false;
    runtime.onlineSince = Date.now() - 10000;
    runtime.bot = {
      entity: { position: { x: 0, y: 64, z: 0 } },
      pathfinder: { setGoal() {} },
      clearControlStates() {},
      stopDigging() {},
      deactivateItem() {},
    };
    runtime.active = { tool: "body_goto", abort: new AbortController() };
    try {
      for (const [i, step] of steps.entries()) {
        runtime.bot.entity.position.x = step;
        runtime.forcedMove({ position: [0, 64, 0], at: Date.now() });
        assert.equal(runtime.paused, i === steps.length - 1);
      }
      assert.equal(runtime.active.abort.signal.aborted, true);
      assert.ok(runtime.recent.at(-1).data.windowDistance > 0.75);
    } finally {
      clearInterval(runtime.safetyTimer);
      clearInterval(runtime.stateTimer);
    }
  }
});

test("idle collision-plane precision does not relatch pause; ordinary idle moves and existing pause remain protected", () => {
  const runtime = new MinecraftBody(
    { minecraft: { username: "MineBodyTest" }, backend: {} },
    () => {},
  );
  runtime.connected = true;
  runtime.ready = true;
  runtime.paused = false;
  runtime.onlineSince = Date.now() - 10000;
  runtime.bot = {
    entity: { position: { x: 0.5, y: 62.535, z: 9.697 } },
    controlState: { forward: false, jump: false },
    blockAt(p) {
      return { position: p, shapes: [[0, 0, 0, 1, 1, 1]] };
    },
    pathfinder: { setGoal() {} },
    clearControlStates() {},
    stopDigging() {},
    deactivateItem() {},
  };
  try {
    runtime.prepared.set("old", { tool: "body_place" });
    runtime.prepared.set("move", { tool: "body_goto" });
    runtime.prepared.set("command", { tool: "body_command" });
    for (let i = 0; i < 20; i++)
      runtime.forcedMove({ position: [0.5, 62.535, 9.7], at: Date.now() });
    assert.equal(runtime.paused, false);
    assert.deepEqual([...runtime.prepared.keys()], ["move", "command"]);
    assert.equal(runtime.recent.at(-1).data.precision, true);
    runtime.paused = true;
    runtime.forcedMove({ position: [0.5, 62.535, 9.7], at: Date.now() });
    assert.equal(runtime.paused, true, "precision never auto-resumes a pause");
    runtime.paused = false;
    runtime.bot.blockAt = () => null;
    runtime.forcedMove({ position: [0.5, 62.535, 9.7], at: Date.now() });
    assert.equal(
      runtime.paused,
      true,
      "same tiny movement with unknown contact still pauses",
    );
    runtime.paused = false;
    runtime.bot.entity.position.z = 10.2;
    runtime.forcedMove({ position: [0.5, 62.535, 9.7], at: Date.now() });
    assert.equal(
      runtime.paused,
      true,
      "ordinary half-block idle movement still pauses",
    );
  } finally {
    clearInterval(runtime.safetyTimer);
    clearInterval(runtime.stateTimer);
  }
});

test("precision-retained commands still revalidate hands before native dispatch", async () => {
  for (const changed of [false, true]) {
    const sent = [];
    let dispatched = 0;
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
    Object.assign(runtime, {
      policy,
      connectionId: "fixture",
      epoch: 1,
      revision: 1,
      ready: true,
      connected: true,
      paused: false,
      lastLease: performance.now(),
      onlineSince: Date.now() - 10000,
    });
    runtime.bot = Object.assign(new EventEmitter(), {
      entity: { position: { x: 0.5, y: 62.535, z: 9.697 } },
      game: { dimension: "overworld" },
      inventory: { slots: Array(46).fill(null) },
      controlState: { forward: false },
      blockAt(p) {
        return { position: p, shapes: [[0, 0, 0, 1, 1, 1]] };
      },
      chat() {
        dispatched++;
      },
      quit() {},
      clearControlStates() {},
      deactivateItem() {},
      stopDigging() {},
      pathfinder: { setGoal() {} },
    });
    try {
      const spec = runtime.describe("body_command", {
        command: "//pos1 1,64,1",
      });
      runtime.prepared.set(spec.id, spec);
      runtime.forcedMove({ position: [0.5, 62.535, 9.7], at: Date.now() });
      assert.equal(runtime.prepared.has(spec.id), true);
      if (changed) runtime.bot.heldItem = { name: "stone" };
      await runtime.call({
        callId: "command",
        connectionId: "fixture",
        controlEpoch: 1,
        policyRevision: 1,
        tool: "body_command",
        args: spec.args,
        specId: spec.id,
        timeoutMs: 1400,
      });
      const result = sent.findLast((x) => x.type === "body.result").data;
      assert.equal(result.code, changed ? "stale" : "");
      assert.equal(dispatched, changed ? 0 : 1);
    } finally {
      await runtime.close();
    }
  }
});
