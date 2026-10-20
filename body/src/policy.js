export const hardBlocks = new Set([
  "bedrock",
  "barrier",
  "command_block",
  "chain_command_block",
  "repeating_command_block",
  "structure_block",
  "structure_void",
  "jigsaw",
  "end_portal_frame",
  "spawner",
]);
export const highRisk = new Set([
  "tnt",
  "tnt_minecart",
  "lava_bucket",
  "water_bucket",
  "flint_and_steel",
  "fire_charge",
  "respawn_anchor",
  "end_crystal",
  "firework_rocket",
  "bow",
  "crossbow",
  "trident",
  "wind_charge",
]);
export const safeFoods = new Set([
  "apple",
  "golden_apple",
  "enchanted_golden_apple",
  "bread",
  "baked_potato",
  "carrot",
  "golden_carrot",
  "cooked_beef",
  "cooked_porkchop",
  "cooked_chicken",
  "cooked_mutton",
  "cooked_rabbit",
  "cooked_cod",
  "cooked_salmon",
  "melon_slice",
  "beetroot",
  "pumpkin_pie",
  "cookie",
  "honey_bottle",
]);
export const readTools = new Set([
  "body_observe",
  "body_inventory",
  "body_find_blocks",
]);
export const nameOf = (s) => (s || "").replace(/^minecraft:/, "");
export const fail = (code) => {
  throw Object.assign(new Error(code), { code });
};
export const vector = (v) =>
  Array.isArray(v) &&
  v.length === 3 &&
  v.every((n) => Number.isFinite(n) && Math.abs(n) <= 30000000);
export const forbiddenText = (s) =>
  typeof s !== "string" || /[\p{Cc}\p{Cf}]/u.test(s);
export function inside(policy, worldId, pos, margin = 0.3) {
  if (!vector(pos) || !worldId) return false;
  if (policy.zones?.mode === "unrestricted") return true;
  if (policy.zones?.mode !== "allowlist") return false;
  return (policy.zones.areas || []).some(
    (a) =>
      a.worldId === worldId &&
      a.min.every((n, i) => pos[i] >= n + margin) &&
      a.max.every((n, i) => pos[i] <= n - margin),
  );
}
export function risk(spec, policy) {
  if (
    readTools.has(spec.tool) ||
    ["body_goto", "body_look_at", "body_say", "body_command"].includes(
      spec.tool,
    )
  )
    return false;
  if (spec.hazards.length) return true;
  return [spec.block, spec.args.item, spec.mainHand, spec.offHand].some(
    (s) =>
      highRisk.has(nameOf(s)) ||
      (policy.highRiskItems || []).some(
        (x) => nameOf(x) === nameOf(s) && !!s,
      ) ||
      (nameOf(s).endsWith("_bed") && spec.dimension !== "minecraft:overworld"),
  );
}
export function checkSpec(spec, policy) {
  if (
    ["body_dig", "body_place"].includes(spec.tool) &&
    (!spec.target || !inside(policy, spec.worldId, spec.target, 0))
  )
    fail("out_of_zone");
  for (const s of [spec.block, spec.args.item, spec.support])
    if (
      hardBlocks.has(nameOf(s)) ||
      (policy.denyBlocks || []).some((x) => nameOf(x) === nameOf(s) && !!s)
    )
      fail("denied_block");
  for (const s of [spec.mainHand, spec.offHand, spec.args.item])
    if ((policy.denyItems || []).some((x) => nameOf(x) === nameOf(s) && !!s))
      fail("denied_item");
  if (
    spec.tool === "body_say" &&
    (forbiddenText(spec.args.text) ||
      spec.args.text.trim().startsWith("/") ||
      /@agent|@ai/i.test(spec.args.text) ||
      Array.from(spec.args.text).length > 256)
  )
    fail("invalid_args");
  if (spec.tool === "body_command") {
    const text = spec.args.command;
    if (
      forbiddenText(text) ||
      !text.trim().replace(/^\/+/, "").trim() ||
      Array.from(text).length > 256
    )
      fail("invalid_args");
    const first = text
      .trim()
      .replace(/^\/+/, "")
      .split(/\s/)[0]
      .toLowerCase()
      .split(":")
      .at(-1)
      .replace(/^\/+/, "");
    if (
      [
        "agent",
        "ai",
        "ma",
        "mineagent",
        ...(policy.loopGuardCommands || []),
      ].includes(first)
    )
      fail("loop_guard");
  }
  if (
    spec.target &&
    ["body_dig", "body_place", "body_interact", "body_use_item"].includes(
      spec.tool,
    ) &&
    Math.hypot(...spec.target.map((n, i) => n - spec.pos[i])) > 5
  )
    fail("not_in_reach");
  if (
    spec.tool === "body_use_item" &&
    !safeFoods.has(nameOf(spec.args.item)) &&
    !risk(spec, policy)
  )
    fail("unsupported_item");
}
export function checkGrant(spec, grant, policy, epoch, now = Date.now()) {
  if (!risk(spec, policy)) return;
  if (
    !grant ||
    !grant.approvalId ||
    grant.specId !== spec.id ||
    grant.controlEpoch !== epoch ||
    grant.expiresAt < now ||
    grant.expiresAt > now + 5100 ||
    !spec.args.reason?.trim() ||
    Array.from(spec.args.reason).length > 60 ||
    forbiddenText(spec.args.reason)
  )
    fail("approval_required");
}
