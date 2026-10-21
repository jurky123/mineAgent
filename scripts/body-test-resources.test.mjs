import test from "node:test";
import assert from "node:assert/strict";
import { checkBodyTestMemory } from "./body-test-resources.mjs";
test("full body fixture rejects missing, malformed or low memory before spawning", () => {
  for (const s of [
    "",
    "MemAvailable: abc kB",
    "MemAvailable: 4194303 kB",
    "MemAvailable: 0 kB",
  ]) {
    assert.throws(() => checkBodyTestMemory(s), /memory_preflight/);
  }
  assert.doesNotThrow(() => checkBodyTestMemory("MemAvailable: 4194304 kB"));
  assert.doesNotThrow(() =>
    checkBodyTestMemory("MemAvailable:    8000000 kB\nSwapFree: 0 kB"),
  );
});

test("verified cgroup hard cap reserves a full GiB and cannot be replaced by an env override", () => {
  const gib = 1024 ** 3;
  assert.doesNotThrow(() =>
    checkBodyTestMemory("MemAvailable: 3145728 kB", 2 * gib),
  );
  assert.throws(
    () => checkBodyTestMemory("MemAvailable: 3145727 kB", 2 * gib),
    /memory_preflight/,
  );
  assert.throws(
    () => checkBodyTestMemory("MemAvailable: 3145728 kB", Infinity),
    /memory_preflight/,
  );
  assert.throws(
    () => checkBodyTestMemory("MemAvailable: 3145728 kB", 3 * gib),
    /memory_preflight/,
  );
  assert.throws(
    () => checkBodyTestMemory("MemAvailable: 3145728 kB", NaN),
    /memory_preflight/,
  );
});
