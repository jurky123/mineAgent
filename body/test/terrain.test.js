import test from "node:test";
import assert from "node:assert/strict";
import { surface, terrain } from "../src/terrain.js";
const bot = (blocks = {}) => ({
  entity: { position: { x: 0.5, y: 64, z: 0.5 } },
  blockAt(p) {
    const name =
      blocks[`${p.x},${p.y},${p.z}`] || (p.y === 63 ? "stone" : "air");
    return {
      name,
      position: p,
      boundingBox: name === "air" ? "empty" : "block",
      shapes: name === "air" ? [] : [[0, 0, 0, 1, 1, 1]],
    };
  },
});
test("terrain gives loaded floors, safe points and headroom", () => {
  const b = bot({ "1,65,0": "stone", "2,64,0": "lava" });
  assert.equal(surface(b, 0, 0).feetY, 64);
  assert.equal(surface(b, 2, 0).hazard, "lava");
  assert.equal(surface(b, 2, 0).standable, false);
  const t = terrain(b);
  assert.equal(t.columns.length, 49);
  assert.equal(t.standablePoints.length, 3);
  assert.ok(t.standablePoints.every((p) => p.distance >= 2.5));
  assert.equal(surface({ entity: b.entity, blockAt: () => null }, 0, 0), null);
});
