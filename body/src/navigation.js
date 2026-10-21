import { Vec3 } from "vec3";
import { openDoorway } from "./terrain.js";

const hazard = /(?:lava|fire|cactus|magma|powder_snow|sweet_berry|wither_rose)/;
const empty = (b) =>
  !!b &&
  !hazard.test(b.name) &&
  b.name !== "water" &&
  (b.boundingBox === "empty" || b.shapes?.length === 0);
const wetBelowFeet = (b, feetY) =>
  b?.getProperties?.().waterlogged === true &&
  b.shapes?.length > 0 &&
  b.shapes.every((s) => b.position.y + s[4] <= feetY + 0.001);

export const wetFootSupport = (bot) =>
  wetBelowFeet(
    bot.blockAt(bot.entity.position.floored()),
    bot.entity.position.y,
  );

// A swimmer can contact a bank with their torso while their feet remain below
// its bottom edge. Require a loaded face intersecting the actual player height.
function fullContactFace(bot, cell, feetY) {
  const height =
    Number.isFinite(bot.entity?.height) && bot.entity.height > 0
      ? Math.min(1.8, bot.entity.height)
      : 1.8;
  const top = feetY + height;
  for (let y = Math.floor(feetY); y < top; y++) {
    const b = bot.blockAt(new Vec3(cell[0], y, cell[2]));
    if (!b) return false;
    if (
      b.shapes?.some(
        (s) =>
          s[0] === 0 &&
          s[2] === 0 &&
          s[3] === 1 &&
          s[5] === 1 &&
          y + s[1] < top &&
          y + s[4] > feetY,
      )
    )
      return true;
  }
  return false;
}

export function contactBackoff(bot) {
  if (!bot.entity.isInWater) return null;
  const p = bot.entity.position,
    cell = [Math.floor(p.x), Math.floor(p.y), Math.floor(p.z)];
  const normal = new Vec3(0, 0, 0);
  for (const axis of [0, 2])
    for (const side of [-1, 1]) {
      const at = axis === 0 ? p.x : p.z;
      if (Math.abs(at - cell[axis] - (side > 0 ? 0.7 : 0.3)) > 0.025) continue;
      const next = [...cell];
      next[axis] += side;
      if (fullContactFace(bot, next, p.y)) {
        if (axis === 0) normal.x -= side;
        else normal.z -= side;
      }
    }
  return normal.norm() ? normal.normalize() : null;
}

// Recognize the client's 0.3/0.7 hitbox clamp versus the server's millimetre
// collision margin. This is a stationary contact correction, not a general
// allowance for idle movement; unknown positions and all other packets fail.
export function contactPrecision(bot, from, to) {
  if (
    Math.abs(from[1] - to[1]) > (bot?.entity?.isInWater ? 0.01 : 1e-6) ||
    !bot?.blockAt ||
    ["forward", "back", "left", "right", "jump", "sprint", "sneak"].some(
      (key) => !!bot.controlState?.[key],
    )
  )
    return false;
  for (const axis of [0, 2]) {
    const d = to[axis] - from[axis],
      other = 2 - axis;
    if (!d || Math.abs(d) > 0.025 || Math.abs(to[other] - from[other]) > 1e-6)
      continue;
    const fraction = from[axis] - Math.floor(from[axis]);
    if (Math.abs(fraction - (d < 0 ? 0.7 : 0.3)) > 1e-6) continue;
    const cell = from.map(Math.floor);
    cell[axis] += d < 0 ? 1 : -1;
    if (fullContactFace(bot, cell, from[1])) return true;
  }
  return false;
}

// Test the whole standing column's horizontal footprint, not just block names.
// A thin ladder beside the player's box leaves real space for an ascent.
const columnOpen = (b, p) =>
  !!b &&
  b.name !== "water" &&
  !hazard.test(b.name) &&
  (empty(b) ||
    (!!b.position &&
      Array.isArray(b.shapes) &&
      b.shapes.every(
        (s) =>
          b.position.x + s[3] <= p.x - 0.3 ||
          b.position.x + s[0] >= p.x + 0.3 ||
          b.position.z + s[5] <= p.z - 0.3 ||
          b.position.z + s[2] >= p.z + 0.3,
      )));
const wetOpenColumn = (b, p) =>
  b?.getProperties?.().waterlogged === true && columnOpen(b, p);

// Recovery only ascends a loaded, unobstructed water column. It never plans a
// submerged horizontal route, moves through a roof, or changes player position.
export function surfaceAbove(bot, maxRise = 16) {
  const p = bot.entity.position;
  const y0 = Math.floor(p.y);
  for (let y = y0; y <= y0 + maxRise; y++) {
    const block = bot.blockAt(new Vec3(Math.floor(p.x), y, Math.floor(p.z)));
    if (
      block?.name === "water" ||
      wetOpenColumn(block, p) ||
      (y === y0 && wetBelowFeet(block, p.y))
    )
      continue;
    const head = bot.blockAt(new Vec3(Math.floor(p.x), y + 1, Math.floor(p.z)));
    if (
      y === y0 ||
      !columnOpen(block, p) ||
      !columnOpen(head, p) ||
      head?.getProperties?.().waterlogged === true
    )
      break;
    for (const dx of [-0.3, 0.3])
      for (const dz of [-0.3, 0.3])
        for (let h = y0; h <= y + 1; h++) {
          const b = bot.blockAt(
            new Vec3(Math.floor(p.x + dx), h, Math.floor(p.z + dz)),
          );
          if (
            b?.name !== "water" &&
            !columnOpen(b, p) &&
            !(h === y0 && wetBelowFeet(b, p.y))
          )
            throw Object.assign(
              Error("上浮水柱未加载或有障碍，请选择其他路线"),
              { code: "surface_recovery_blocked" },
            );
        }
    return y - 1;
  }
  throw Object.assign(Error("16格内没有已知开放水面，不能安全上浮"), {
    code: "surface_recovery_blocked",
  });
}

// Bounded, read-only goal resolution. Inspect every column in the arrival
// region, rather than insisting that a wall's own column be standable.
export function resolveDestination(bot, args) {
  const range = args.range || 2;
  const mode = args.movement || "ground";
  if (!["ground", "swim"].includes(mode))
    throw Object.assign(Error("unsupported_movement"), {
      code: "unsupported_movement",
    });
  const near = args.y ?? bot.entity.position.y;
  const candidates = [],
    alternatives = [];
  let loaded = false,
    water = false;
  for (let x = Math.floor(args.x - range); x <= Math.floor(args.x + range); x++)
    for (
      let z = Math.floor(args.z - range);
      z <= Math.floor(args.z + range);
      z++
    ) {
      const horizontal = Math.hypot(x + 0.5 - args.x, z + 0.5 - args.z);
      if (horizontal > range) continue;
      for (let y = Math.floor(near) - 32; y <= Math.floor(near) + 32; y++) {
        const b = bot.blockAt(new Vec3(x, y, z));
        if (!b) continue;
        loaded = true;
        let feetY;
        if (b.name === "water") {
          if (
            !empty(bot.blockAt(new Vec3(x, y + 1, z))) ||
            !empty(bot.blockAt(new Vec3(x, y + 2, z)))
          )
            continue;
          water = true;
          if (mode !== "swim") continue;
          feetY = y; // head remains above the top surface, never a submerged goal
        } else {
          if (b.boundingBox !== "block" || hazard.test(b.name)) continue;
          const top = b.shapes?.length
            ? Math.max(...b.shapes.map((s) => s[4]))
            : 1;
          if (top <= 0 || top > 1) continue;
          // An ascent node inside a ladder is not a standing floor at its
          // block centre. Require actual support under the player's box.
          if (
            b.shapes?.length &&
            !b.shapes.some(
              (s) =>
                s[4] === top &&
                s[0] < 0.8 &&
                s[3] > 0.2 &&
                s[2] < 0.8 &&
                s[5] > 0.2,
            )
          )
            continue;
          feetY = y + top;
          const feet = bot.blockAt(new Vec3(x, Math.floor(feetY + 0.001), z));
          const head = bot.blockAt(new Vec3(x, Math.floor(feetY + 1), z));
          if (
            (top >= 1 && !empty(feet) && !openDoorway(feet)) ||
            (!empty(head) && !openDoorway(head))
          )
            continue;
        }
        const point = [x + 0.5, feetY, z + 0.5];
        const score = Math.abs(feetY - near) * 4 + horizontal;
        alternatives.push({ point, score });
        if (
          args.y === undefined ||
          Math.hypot(horizontal, feetY - args.y) <= range
        )
          candidates.push({ point, score });
      }
    }
  candidates.sort((a, b) => a.score - b.score);
  alternatives.sort((a, b) => a.score - b.score);
  if (!candidates.length) {
    let code = !loaded
      ? "terrain_unknown"
      : water && mode === "ground"
        ? "water_only"
        : "no_standable_surface";
    const closest = alternatives[0];
    if (closest && args.y !== undefined)
      code =
        args.y > closest.point[1]
          ? "target_above_ground"
          : "target_below_ground";
    throw Object.assign(
      Error(
        "目标范围内没有符合当前移动模式的安全落点；请使用建议高度、邻近目标或明确的swim模式，不要盲试坐标",
      ),
      {
        code,
        data: {
          movement: mode,
          x: args.x,
          z: args.z,
          ...(closest
            ? { suggestedY: closest.point[1], alternative: closest.point }
            : {}),
        },
      },
    );
  }
  return {
    target: [args.x, args.y ?? candidates[0].point[1], args.z],
    movement: mode,
    candidates: candidates.map((c) => c.point),
  };
}
