import { Vec3 } from "vec3";

const unsafe =
  /(?:water|lava|fire|cactus|magma|powder_snow|sweet_berry|wither_rose)/;
export const openDoorway = (b) =>
  !!b?.name?.endsWith("_door") &&
  b.getProperties?.().open === true &&
  Array.isArray(b.shapes) &&
  b.shapes.every(
    (s) => s[3] <= 0.2 || s[0] >= 0.8 || s[5] <= 0.2 || s[2] >= 0.8,
  );

// An open leaf is passable along its doorway, but still blocks sideways
// crossings. Check the real swept player box for each proposed edge.
export function doorTransitionClear(bot, from, to) {
  const a = [Math.floor(from.x) + 0.5, from.y, Math.floor(from.z) + 0.5];
  const c = [Math.floor(to.x) + 0.5, to.y, Math.floor(to.z) + 0.5];
  const min = [
    Math.min(a[0], c[0]) - 0.3,
    Math.min(a[1], c[1]),
    Math.min(a[2], c[2]) - 0.3,
  ];
  const max = [
    Math.max(a[0], c[0]) + 0.3,
    Math.max(a[1], c[1]) + 1.8,
    Math.max(a[2], c[2]) + 0.3,
  ];
  for (let x = Math.floor(min[0]); x <= Math.floor(max[0]); x++)
    for (let y = Math.floor(min[1]); y <= Math.floor(max[1]); y++)
      for (let z = Math.floor(min[2]); z <= Math.floor(max[2]); z++) {
        const b = bot.blockAt(new Vec3(x, y, z));
        if (!b) return false;
        if (!b.name?.endsWith("_door")) continue;
        if (
          b.shapes?.some(
            (s) =>
              x + s[0] < max[0] - 1e-7 &&
              x + s[3] > min[0] + 1e-7 &&
              y + s[1] < max[1] - 1e-7 &&
              y + s[4] > min[1] + 1e-7 &&
              z + s[2] < max[2] - 1e-7 &&
              z + s[5] > min[2] + 1e-7,
          )
        )
          return false;
      }
  return true;
}

const free = (b) =>
  !!b &&
  !unsafe.test(b.name) &&
  (b.boundingBox === "empty" ||
    (Array.isArray(b.shapes) && b.shapes.length === 0) ||
    openDoorway(b));

// Only loaded blocks within eight vertical blocks of the body are inspected.
// This chooses a nearby floor, not an unbounded world's highest roof.
export function surface(bot, x, z, nearY = bot.entity.position.y) {
  const bx = Math.floor(x),
    bz = Math.floor(z),
    candidates = [];
  for (let y = Math.floor(nearY) - 8; y <= Math.floor(nearY) + 8; y++) {
    const b = bot.blockAt(new Vec3(bx, y, bz));
    if (!b || b.boundingBox !== "block" || unsafe.test(b.name)) continue;
    const top = b.shapes?.length ? Math.max(...b.shapes.map((s) => s[4])) : 1;
    if (top <= 0) continue;
    const feetY = y + top;
    const feet = bot.blockAt(new Vec3(bx, Math.floor(feetY + 0.001), bz));
    const head = bot.blockAt(new Vec3(bx, Math.floor(feetY + 1), bz));
    const feetFree = top < 1 || free(feet),
      headFree = free(head);
    candidates.push({
      surfaceY: y,
      feetY,
      feetFree,
      headFree,
      standable: feetFree && headFree,
      hazard: [feet, head].find((v) => v && unsafe.test(v.name))?.name || null,
    });
  }
  return (
    candidates
      .filter((v) => v.standable)
      .sort(
        (a, b) => Math.abs(a.feetY - nearY) - Math.abs(b.feetY - nearY),
      )[0] ||
    candidates.sort(
      (a, b) => Math.abs(a.feetY - nearY) - Math.abs(b.feetY - nearY),
    )[0] ||
    null
  );
}

export function terrain(bot) {
  const p = bot.entity.position,
    cx = Math.floor(p.x),
    cz = Math.floor(p.z);
  const columns = [],
    points = [];
  for (let dx = -3; dx <= 3; dx++)
    for (let dz = -3; dz <= 3; dz++) {
      const s = surface(bot, cx + dx, cz + dz);
      columns.push({
        dx,
        dz,
        surfaceY: s?.surfaceY ?? null,
        feetFree: !!s?.feetFree,
        headFree: !!s?.headFree,
        standable: !!s?.standable,
        hazard: s?.hazard || null,
      });
      if (s?.standable && Math.abs(s.feetY - p.y) <= 2) {
        const position = [cx + dx + 0.5, s.feetY, cz + dz + 0.5];
        const distance = Math.hypot(
          position[0] - p.x,
          position[1] - p.y,
          position[2] - p.z,
        );
        if (distance >= 2.5)
          points.push({ position, distance: Math.round(distance * 100) / 100 });
      }
    }
  return {
    radius: 3,
    columns,
    standablePoints: points.sort((a, b) => a.distance - b.distance).slice(0, 3),
  };
}
