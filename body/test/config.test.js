import test from "node:test";
import assert from "node:assert/strict";
import { validateConfig } from "../src/config.js";
const fixture = {
  backend: { url: "ws://127.0.0.1:8765/ws" },
  minecraft: {
    host: "127.0.0.1",
    port: 25566,
    username: "MineBodyTest",
    auth: "offline",
    version: "26.1",
    targetVersion: "26.2",
    viaProxy: true,
  },
};
const env = {
  MINEAGENT_BODY_TOKEN:
    "fixture-independent-body-credential-not-for-production",
};
test("body requests bounded chunk distance instead of Mineflayer far default", () => {
  assert.equal(validateConfig(fixture, env).minecraft.viewDistance, 4);
  for (const viewDistance of [2, 4, 8])
    assert.equal(
      validateConfig(
        { ...fixture, minecraft: { ...fixture.minecraft, viewDistance } },
        env,
      ).minecraft.viewDistance,
      viewDistance,
    );
  for (const viewDistance of [0, 1, 9, 2.5, "tiny"])
    assert.throws(
      () =>
        validateConfig(
          { ...fixture, minecraft: { ...fixture.minecraft, viewDistance } },
          env,
        ),
      /viewDistance/,
    );
  assert.equal(fixture.minecraft.viewDistance, undefined);
});
