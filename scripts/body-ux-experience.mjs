// Opt-in, real model / real isolated Paper UX acceptance. No production writes.
import fs from "node:fs/promises";
import path from "node:path";
import assert from "node:assert/strict";
import { createRequire } from "node:module";
const require = createRequire(new URL("../body/package.json", import.meta.url));
const mineflayer = require("mineflayer");
export async function runExperience({
  page,
  origin,
  api,
  state,
  control,
  command,
  controller,
  until,
  wait,
  temp,
  uxModelMetrics,
}) {
  const report = {
    at: new Date().toISOString(),
    model: "existing configured model",
    samples: [],
  };
  const persist = () =>
    fs.writeFile(
      path.join(temp, "ux-experience.json"),
      JSON.stringify(report, null, 2),
      { mode: 0o600 },
    );
  async function mc(bot, text, label) {
    const messages = [];
    const onMessage = (m) => messages.push(m);
    bot.on("messagestr", onMessage);
    const start = Date.now();
    try {
      bot.chat("@agent [UX REAL] " + text);
      const reply = await until(
        () => messages.find((m) => m.includes("[MineAgent]")),
        "MC real-model reply",
        90000,
      );
      const result = { label, elapsedMs: Date.now() - start, reply };
      report.samples.push(result);
      await persist();
      return result;
    } finally {
      bot.off("messagestr", onMessage);
    }
  }
  async function task(text, label, check = () => {}, during) {
    await page.locator("#body-task-new").click();
    const start = Date.now(),
      metricStart = uxModelMetrics.length;
    await page.locator("#body-task-input").fill("[UX REAL] " + text);
    const response = page.waitForResponse(
      (r) =>
        r.request().method() === "POST" &&
        r.url().includes("/api/body/task/send"),
    );
    await page.locator("#body-task-send").click();
    const sent = await (await response).json();
    assert.ok(sent.conv, "web task accepted");
    const snapshots = [];
    let history;
    if (during) await during(sent.conv);
    history = await until(
      async () => {
        const h = await api("/api/body/task/history?conv=" + sent.conv);
        snapshots.push({
          elapsedMs: Date.now() - start,
          progress: await page.locator("#body-run-status").innerText(),
          events: h.runEvents?.length || 0,
        });
        return !h.running && h.messages?.some((m) => m.role === "agent")
          ? h
          : null;
      },
      "real-model web task " + label,
      120000,
    );
    const events = history.runEvents || [];
    const sample = {
      label,
      elapsedMs: Date.now() - start,
      reply: history.messages
        .filter((m) => m.role === "agent")
        .map((m) => m.text)
        .join("\n"),
      events,
      metrics: uxModelMetrics.slice(metricStart),
      progress: snapshots
        .filter((v, i, a) => i === 0 || v.progress !== a[i - 1].progress)
        .slice(-20),
    };
    report.samples.push(sample);
    await persist();
    await check(sample);
    return sample;
  }
  const visitor = mineflayer.createBot({
    host: "127.0.0.1",
    port: 25666,
    username: "BodyVisitor",
    auth: "offline",
    version: "26.1",
    checkTimeoutInterval: 30000,
  });
  visitor.on("error", () => {});
  try {
    await until(() => visitor.entity, "public visitor spawn", 30000);
    await wait(1000);
    const awareness = await mc(
      visitor,
      "你在哪、在做什么？只答一句公开身体状态，不要坐标。",
      "MC public awareness",
    );
    assert.ok(awareness.reply.includes("MineBodyV1"));
    assert.ok(!/我没有.*身体|我不在游戏世界/.test(awareness.reply));
    const denied = await mc(
      visitor,
      "请移动到我身边。",
      "MC unverified action",
    );
    assert.ok(/关联|管理员|ai\/main|授权/.test(denied.reply));
  } finally {
    visitor.quit();
  }
  await mc(
    controller,
    "只调用一次 body_observe 并一句话报告当前状态。",
    "MC verified observe",
  );
  await page.goto(origin + "/ai/main");
  await page.locator("#body-task-send").waitFor();
  await command("tp MineBodyV1 0 -60 0");
  await command("fill -10 -60 -10 32 -57 16 air");
  await wait(1200);
  await control("resume");
  for (let i = 0; i < 3; i++) {
    await task(
      "观察附近地形一次，从 terrain.standablePoints 选择最近的约3格可站立点，调用一次 body_goto 走过去（range=1），一句话汇报。不要查找方块，不要再次观察。",
      "terrain move " + i,
    );
  }
  for (let i = 0; i < 3; i++) {
    const pos = (await state()).state.pos;
    await task(
      `直接走到 x=${Math.floor(pos[0]) + 3},z=${Math.floor(pos[2])}，y省略，range=1。只调用一次 body_goto，不要先观察或查询，最后一句话汇报。`,
      "direct goto " + i,
    );
  }
  await task(
    "以身体身份执行一次 /help，准确区分已提交和实际回显，不要查询服务器或玩家。",
    "help",
  );
  await task("取消当前动作，调用一次 body_stop，然后结束。", "body_stop");
  assert.equal((await state()).paused, false);
  // A real authenticated pause while a tool is running. Resume the same conversation.
  await task(
    "直接走到 x=28,z=10，y省略，range=1，只走一次。",
    "pause/resume",
    () => {},
    async (conv) => {
      await until(
        async () => (await state()).state.task?.tool === "body_goto",
        "active move before pause",
        30000,
      );
      await control("pause");
      await wait(600);
      const paused = await state();
      assert.equal(paused.paused, true);
      assert.ok(
        (await api("/api/body/task/history?conv=" + conv)).running,
        "original task must remain live",
      );
      await until(async () => !(await state()).busy, "pause cleanup");
      await control("resume");
    },
  );
  await task(
    "只给一个建造行动计划，不执行。我要在出生点512格保护区外用WorldEdit建10乘10小屋；说明如何指定选区、批量执行、验证和回滚。不得建议逐块放置250次，也不要修改保护。",
    "build/WorldEdit plan",
  );
  report.summary = {
    terrainP50: median(
      report.samples
        .filter((s) => s.label.startsWith("terrain move"))
        .map((s) => s.elapsedMs),
    ),
    directP50: median(
      report.samples
        .filter((s) => s.label.startsWith("direct goto"))
        .map((s) => s.elapsedMs),
    ),
  };
  await persist();
  await page.screenshot({
    path: path.join(temp, "ux-ai-main.png"),
    fullPage: true,
  });
  console.log(
    "Real UX report: " +
      JSON.stringify({
        path: path.join(temp, "ux-experience.json"),
        ...report.summary,
      }),
  );
}
function median(a) {
  return [...a].sort((a, b) => a - b)[Math.floor(a.length / 2)];
}
