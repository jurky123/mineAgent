import { Vec3 } from "vec3";

const unsafe =
  /(?:water|lava|fire|cactus|magma|powder_snow|sweet_berry|wither_rose)/;
const free = (b) =>
  !!b &&
  !unsafe.test(b.name) &&
  (b.boundingBox === "empty" ||
    (Array.isArray(b.shapes) && b.shapes.length === 0));

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
