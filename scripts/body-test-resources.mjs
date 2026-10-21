// Full-stack tests need a reserved margin as well as an enforceable hard cap.
import fs from "node:fs/promises";
const GiB = 1024 ** 3;
export function checkBodyTestMemory(meminfo, hardLimit = Infinity) {
  const kib = Number(meminfo.match(/^MemAvailable:\s+(\d+)\s+kB$/m)?.[1]);
  const capped =
    Number.isFinite(hardLimit) && hardLimit > 0 && hardLimit <= 2 * GiB;
  const required = capped ? hardLimit + GiB : 4 * GiB;
  if (!Number.isFinite(kib) || kib * 1024 < required)
    throw Error(
      "memory_preflight: full body E2E requires 4 GiB MemAvailable, or a verified cgroup cap <=2 GiB plus 1 GiB reserved headroom. No process was launched.",
    );
}
export async function bodyTestResourcePreflight() {
  if (process.platform !== "linux") return;
  let limit = Infinity;
  const group = (await fs.readFile("/proc/self/cgroup", "utf8"))
    .split("\n")
    .find((s) => s.startsWith("0::"))
    ?.slice(3);
  if (group?.startsWith("/") && !group.split("/").includes("..")) {
    let at = group;
    for (;;) {
      try {
        const v = (
          await fs.readFile(
            "/sys/fs/cgroup" + at.replace(/\/$/, "") + "/memory.max",
            "utf8",
          )
        ).trim();
        if (/^\d+$/.test(v)) limit = Math.min(limit, Number(v));
      } catch {}
      if (at === "/") break;
      at = at.slice(0, at.lastIndexOf("/")) || "/";
    }
  }
  checkBodyTestMemory(await fs.readFile("/proc/meminfo", "utf8"), limit);
}
