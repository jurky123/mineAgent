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
