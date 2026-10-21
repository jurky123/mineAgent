import { Vec3 } from "vec3";

const hazard = /(?:lava|fire|cactus|magma|powder_snow|sweet_berry|wither_rose)/;
const empty = (b) =>
  !!b &&
  !hazard.test(b.name) &&
  b.name !== "water" &&
  (b.boundingBox === "empty" || b.shapes?.length === 0);

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
