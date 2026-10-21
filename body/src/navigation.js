import { Vec3 } from "vec3";

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

// Recognize the client's 0.3/0.7 hitbox clamp versus the server's millimetre
// collision margin. This is a stationary contact correction, not a general
// allowance for idle movement; unknown positions and all other packets fail.
export function contactPrecision(bot, from, to) {
  if (
    Math.abs(from[1] - to[1]) > 1e-6 ||
    !bot?.blockAt ||
    Object.values(bot.controlState || {}).some(Boolean)
  )
    return false;
  for (const axis of [0, 2]) {
    const d = to[axis] - from[axis],
      other = 2 - axis;
    if (!d || Math.abs(d) > 0.01 || Math.abs(to[other] - from[other]) > 1e-6)
      continue;
    const fraction = from[axis] - Math.floor(from[axis]);
    if (Math.abs(fraction - (d < 0 ? 0.7 : 0.3)) > 1e-6) continue;
    const cell = from.map(Math.floor);
    cell[axis] += d < 0 ? 1 : -1;
    const b = bot.blockAt(new Vec3(...cell)),
      feet = from[1] - cell[1];
    if (
      b?.shapes?.some(
        (s) =>
          s[0] === 0 &&
          s[2] === 0 &&
          s[3] === 1 &&
          s[5] === 1 &&
          s[1] <= feet &&
          s[4] > feet,
      )
    )
      return true;
  }
  return false;
}

// Recovery only ascends a loaded, unobstructed water column. It never plans a
// submerged horizontal route, moves through a roof, or changes player position.
export function surfaceAbove(bot, maxRise = 16) {
  const p = bot.entity.position;
  const y0 = Math.floor(p.y);
  for (let y = y0; y <= y0 + maxRise; y++) {
    const block = bot.blockAt(new Vec3(Math.floor(p.x), y, Math.floor(p.z)));
    if (block?.name === "water" || (y === y0 && wetBelowFeet(block, p.y)))
      continue;
    if (
      y === y0 ||
      !empty(block) ||
      !empty(bot.blockAt(new Vec3(Math.floor(p.x), y + 1, Math.floor(p.z))))
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
            !empty(b) &&
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
          feetY = y + top;
          const feet = bot.blockAt(new Vec3(x, Math.floor(feetY + 0.001), z));
          const head = bot.blockAt(new Vec3(x, Math.floor(feetY + 1), z));
          if ((top >= 1 && !empty(feet)) || !empty(head)) continue;
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
    candidates: candidates.slice(0, 8).map((c) => c.point),
  };
}
