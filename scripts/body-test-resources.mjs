// Full Paper + ViaProxy + Mineflayer + browser peaks substantially above -Xmx.
// Fail before spawning processes on shared machines with insufficient headroom.
import fs from "node:fs/promises";
export function checkBodyTestMemory(meminfo) {
  const kib = Number(meminfo.match(/^MemAvailable:\s+(\d+)\s+kB$/m)?.[1]);
  if (!Number.isFinite(kib) || kib < 4 * 1024 * 1024) {
    throw Error(
      "memory_preflight: full body E2E needs at least 4 GiB MemAvailable before startup; use a dedicated test host. No process was launched.",
    );
  }
}
export async function bodyTestResourcePreflight() {
  if (process.platform === "linux")
    checkBodyTestMemory(await fs.readFile("/proc/meminfo", "utf8"));
}
