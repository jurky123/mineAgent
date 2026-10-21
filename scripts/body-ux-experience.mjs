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
    failures: [],
  };
  const persist = () =>
    fs.writeFile(
      path.join(temp, "ux-experience.json"),
      JSON.stringify(report, null, 2),
      { mode: 0o600 },
    );
  const check = async (label, fn) => {
    try {
      await fn();
    } catch (e) {
      report.failures.push({ label, error: e.message });
      await persist();
    }
  };
  async function mc(bot, text, label) {
    const messages = [];
    const onMessage = (m) => messages.push(m);
    bot.on("messagestr", onMessage);
    const start = Date.now(),
      metricStart = uxModelMetrics.length;
    try {
      bot.chat("@agent [UX REAL] " + text);
      const reply = await until(
        () => messages.find((m) => m.includes("[MineAgent]")),
        "MC real-model reply",
        90000,
      );
      const result = {
        label,
        elapsedMs: Date.now() - start,
        reply,
        metrics: uxModelMetrics.slice(metricStart),
      };
      report.samples.push(result);
      await persist();
      return result;
    } finally {
      bot.off("messagestr", onMessage);
    }
  }
  async function task(text, label, verify = () => {}, during) {
    await until(
      async () => await page.locator("#body-task-stop").isDisabled(),
      "previous web task idle",
    );
    // This button changes local conversation state; it never navigates.
    await page.locator("#body-task-new").click({ noWaitAfter: true });
    await page
      .locator("#body-run-status")
      .getByText("新任务会话，发送前不会创建记录。", { exact: true })
      .waitFor();
    const initialPosition = (await state()).state.pos;
    const start = Date.now(),
      metricStart = uxModelMetrics.length;
    await page.locator("#body-task-input").fill("[UX REAL] " + text);
    const response = page.waitForResponse(
      (r) =>
        r.request().method() === "POST" &&
        r.url().includes("/api/body/task/send"),
    );
    await page.locator("#body-task-send").click({ noWaitAfter: true });
    const sent = await (await response).json();
    assert.ok(sent.conv, "web task accepted");
    const snapshots = [];
    let history;
    if (during) await check(label + " control", () => during(sent.conv));
    history = await until(
      async () => {
        const h = await api(
          "/api/body/task/history?agent=main&conv=" + sent.conv,
        );
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
      initialPosition,
      finalPosition: (await state()).state.pos,
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
    sample.movedDistance = Math.hypot(
      ...sample.finalPosition.map((v, i) => v - sample.initialPosition[i]),
    );
    sample.toolNames = events.map((e) => e.name);
    sample.readCalls = sample.toolNames.filter((n) =>
      [
        "body_status",
        "body_observe",
        "body_inventory",
        "body_find_blocks",
      ].includes(n),
    ).length;
    const finalReply = history.messages.findLast(
      (m) => m.role === "agent",
    )?.text;
    if (finalReply) {
      await page
        .locator("#body-replies")
        .getByText(finalReply, { exact: true })
        .waitFor();
      sample.visibleElapsedMs = Date.now() - start;
    }
    report.samples.push(sample);
    await persist();
    await check(label, () => verify(sample));
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
    await check(awareness.label, () => {
      assert.ok(/MineBodyV1|身体/.test(awareness.reply));
      assert.ok(/在线|online/i.test(awareness.reply));
      assert.ok(/idle|空闲|待机|挂机|没在跑动作/.test(awareness.reply));
      assert.ok(!/我没有.*身体|我不在游戏世界/.test(awareness.reply));
    });
    const denied = await mc(
      visitor,
      "请移动到我身边。",
      "MC unverified action",
    );
    await check(denied.label, () => {
      assert.ok(/关联|管理员|ai\/main|授权/.test(denied.reply));
    });
  } finally {
    visitor.quit();
  }
  const verified = await mc(
    controller,
    "只调用一次 body_observe 并一句话报告当前状态。",
    "MC verified observe",
  );
  await check(verified.label, () =>
    assert.ok(verified.metrics.some((m) => m.tools.includes("body_observe"))),
  );
  await page.goto(origin + "/ai/main");
  await page.locator("#body-task-send").waitFor();
  const buildPlan = () =>
    task(
      "只给一个建造行动计划，不执行。我要在出生点512格保护区外用WorldEdit建10乘10小屋；说明如何指定选区、批量执行、验证和回滚。不得建议逐块放置250次，也不要修改保护。",
      "build/WorldEdit plan",
      (s) => {
        assert.ok(
          /WorldEdit|选区/i.test(s.reply),
          "must provide the requested build plan",
        );
        assert.ok(/512|保护/.test(s.reply), "must retain spawn protection");
        assert.ok(
          /矩形|包围盒|不相交/.test(s.reply),
          "must apply rectangular whole-footprint protection",
        );
        assert.ok(
          !s.toolNames.includes("body_command"),
          "plan must not execute commands",
        );
        assert.ok(s.movedDistance < 0.01, "plan must not move the body");
      },
    );
  if (process.env.MINEAGENT_UX_RECHECK === "1") {
    report.scope = "MC and build-plan recheck; no movement latency samples";
    await buildPlan();
    await persist();
    await page.screenshot({
      path: path.join(temp, "ux-recheck.png"),
      fullPage: true,
    });
    assert.equal(report.failures.length, 0, "targeted real UX recheck failed");
    return;
  }
  await command("tp MineBodyV1 0 -60 0");
  await command("fill -10 -60 -10 32 -57 16 air");
  await wait(1200);
  await control("resume");
  for (let i = 0; i < 3; i++) {
    await task(
      "观察附近地形一次，根据 terrain 选一个距你4–5格的安全可站立点（7×7网格的对角位置也可），调用一次 body_goto 走过去（range=1，实际步行约3格），一句话汇报。不要查找方块，不要再次观察。",
      "terrain move " + i,
      (s) => {
        assert.ok(s.toolNames.includes("body_goto"), "must invoke movement");
        assert.ok(
          s.readCalls >= 1 && s.readCalls <= 2,
          "one or two observations",
        );
        assert.ok(s.movedDistance >= 2, "body must actually move");
      },
    );
  }
  for (let i = 0; i < 3; i++) {
    const pos = (await state()).state.pos;
    await task(
      `直接走到 x=${Math.floor(pos[0]) + 4},z=${Math.floor(pos[2])}，y省略，range=1。只调用一次 body_goto，不要先观察或查询，最后一句话汇报。`,
      "direct goto " + i,
      (s) => {
        assert.ok(s.toolNames.includes("body_goto"), "must invoke movement");
        assert.equal(s.readCalls, 0, "explicit coordinates need no extra scan");
        assert.ok(s.movedDistance >= 2, "body must actually move");
      },
    );
  }
  await task(
    "以身体身份执行一次 /help，准确区分已提交和实际回显，不要查询服务器或玩家。",
    "help",
    (s) => assert.ok(s.toolNames.includes("body_command")),
  );
  await task("取消当前动作，调用一次 body_stop，然后结束。", "body_stop");
  assert.equal((await state()).paused, false);
  // A real authenticated pause while a tool is running. Resume the same conversation.
  await task(
    "直接走到 x=28,z=10，y省略，range=1。若管理员暂停，恢复后在当前任务中继续目标，不要要求重新提交。",
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
        (await api("/api/body/task/history?agent=main&conv=" + conv)).running,
        "original task must remain live",
      );
      await until(async () => !(await state()).busy, "pause cleanup");
      await control("resume");
    },
  );
  await buildPlan();
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
  await check("terrain latency", () =>
    assert.ok(report.summary.terrainP50 < 15000),
  );
  await check("direct latency", () =>
    assert.ok(report.summary.directP50 < 5000),
  );
  assert.equal(
    report.failures.length,
    0,
    "real UX acceptance failures; see ux-experience.json",
  );
}
function median(a) {
  return [...a].sort((a, b) => a - b)[Math.floor(a.length / 2)];
}
