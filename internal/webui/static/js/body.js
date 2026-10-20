import { boot, apiGet, apiPost } from "./shell.js";
import { h, toast, withPending, confirmDialog } from "./ds.js";
const $ = (id) => document.getElementById(id);
const errors = {
  not_operator: "需要已认证的管理员账号。",
  body_paused: "身体已暂停。",
  stale_approval: "这项审批已过期或目标状态已改变，请重新发起。",
  stop_unconfirmed: "停止状态尚未确认，请核查身体状态。",
  body_not_ready: "身体尚未准备好，请稍后再试。",
  origin_rejected: "当前页面来源不受信任。",
};
const labels = {
  body_goto: "移动",
  body_dig: "挖掘",
  body_place: "放置",
  body_interact: "交互",
  body_use_item: "使用物品",
  body_say: "聊天",
  body_command: "执行命令",
  spawn: "进入游戏",
  offline: "离线",
  paused: "暂停",
  death: "死亡",
  kicked: "被服务器断开",
  chat: "游戏聊天",
  error: "连接异常",
};
let refreshing = false,
  stream;
function readGameCode() {
  const gameCode = new URLSearchParams(location.hash.slice(1)).get("game-code");
  if (gameCode) {
    if (/^[0-9a-f]{32}$/.test(gameCode)) $("body-game-code").value = gameCode;
    history.replaceState(null, "", location.pathname);
  }
}
readGameCode();
window.addEventListener("hashchange", readGameCode);
$("body-game-link").onsubmit = (event) => {
  event.preventDefault();
  withPending($("body-game-approve"), async () => {
    const code = $("body-game-code").value.trim();
    if (!/^[0-9a-f]{32}$/.test(code)) {
      toast("请输入游戏面板中的 32 位临时代码。");
      return;
    }
    try {
      const preview = await apiPost("/api/body/minecraft/link", {
        action: "preview",
        code,
      });
      if (
        !(await confirmDialog({
          title: "授权当前游戏连接",
          body: `玩家：${preview.player}\nUUID：${preview.uuid}\n允许在本次连接中查看完整身体状态、下达任务、审批和控制 AI 玩家，最长 10 分钟。请确认这是你当前的游戏连接。`,
          confirmText: "授权此连接",
        }))
      )
        return;
      await apiPost("/api/body/minecraft/link", {
        action: "approve",
        code,
        uuid: preview.uuid,
        session: preview.session,
      });
      $("body-game-code").value = "";
      $("body-game-result").textContent =
        "已授权。回到游戏面板刷新后即可使用，离线或 10 分钟后失效。";
    } catch (e) {
      $("body-game-result").textContent =
        "未能确认授权，请检查临时代码是否过期，并在游戏面板刷新核查。";
    }
  });
};
$("body-game-revoke").onclick = () =>
  withPending($("body-game-revoke"), async () => {
    try {
      await apiPost("/api/body/minecraft/link", { action: "revoke" });
      $("body-game-result").textContent = "已撤销本次网页登录授予的游戏控制。";
    } catch (e) {
      showError(e);
    }
  });
function showError(e) {
  $("body-error").hidden = false;
  $("body-error").textContent =
    errors[e.message] || "暂时无法取得确认，请查看最新状态。";
}
function render(st) {
  const s = st.state || {};
  const stale = st.stale || !st.controlConnected;
  $("body-name").textContent = st.name || "AI 玩家";
  $("body-status").textContent = stale
    ? "状态未知"
    : !s.connected
      ? "机器人离线"
      : st.draining
        ? "正在准备更新"
        : st.paused
          ? "身体已暂停"
          : s.task
            ? "正在行动"
            : "在线待机";
  $("body-hint").textContent = stale
    ? "当前数据可能已过期。"
    : st.mode === "observe"
      ? "观察模式：只提供状态查询。"
      : st.paused
        ? "恢复后请重新发起任务，旧任务不会继续。"
        : "通过 Agent 聊天下达明确任务，高危动作在此审核。";
  $("body-task").textContent = s.task
    ? labels[s.task.tool] || "执行动作"
    : "待机";
  $("body-health").textContent = s.connected ? `${s.health} / ${s.food}` : "—";
  $("body-item").textContent = s.heldItem || "空手";
  $("body-position").textContent = s.connected
    ? `${s.worldId || s.dimension || "世界未知"} · ${(s.pos || []).map((n) => Number(n).toFixed(1)).join(", ")}`
    : "—";
  $("body-updated").textContent =
    st.receivedAt > 0
      ? new Date(st.receivedAt).toLocaleTimeString()
      : "尚无状态";
  $("body-resume").disabled = !st.ready || stale || !!st.busy;
  const list = $("body-events");
  list.replaceChildren();
  for (const e of (st.events || []).slice(-12).reverse()) {
    const row = h("li");
    const time = h("time", null, new Date(e.at).toLocaleTimeString());
    row.append(time, document.createTextNode(labels[e.type] || "状态更新"));
    if (e.type === "chat") {
      const text = h(
        "span",
        null,
        ` · ${e.data?.player || "玩家"}：${e.data?.message || ""}`,
      );
      row.append(text);
    }
    list.append(row);
  }
  if (!list.children.length) list.append(h("li", null, "暂无事件"));
}
async function approvals() {
  const data = await apiGet("/api/body/approvals");
  const list = $("body-approvals");
  list.replaceChildren();
  $("body-pending-count").textContent = String(data.approvals.length);
  for (const a of data.approvals) {
    const row = h("article", "body-approval");
    row.append(h("p", null, a.prompt));
    const actions = h("div", "body-buttons");
    for (const [text, approved] of [
      ["批准本次动作", true],
      ["拒绝", false],
    ]) {
      const b = h("button", approved ? "btn primary" : "btn", text);
      b.onclick = () =>
        withPending(b, async () => {
          try {
            await apiPost(
              "/api/body/approvals/" + encodeURIComponent(a.approvalId),
              { approved },
            );
            toast(approved ? "已批准，等待实际执行结果" : "已拒绝");
            await refresh();
          } catch (e) {
            showError(e);
          }
        });
      actions.append(b);
    }
    row.append(actions);
    list.append(row);
  }
  if (!data.approvals.length)
    list.append(h("p", "field-hint", "暂无待审核动作。"));
}
async function refresh() {
  if (refreshing) return;
  refreshing = true;
  try {
    render(await apiGet("/api/body/status"));
    await approvals();
  } catch (e) {
    showError(e);
  } finally {
    refreshing = false;
  }
}
for (const [id, action] of [
  ["body-stop", "stop"],
  ["body-resume", "resume"],
  ["body-disconnect", "disconnect"],
])
  $(id).onclick = () =>
    withPending($(id), async () => {
      if (
        action === "disconnect" &&
        !(await confirmDialog({
          title: "离开游戏",
          body: "AI 玩家会离开游戏。之后可点击恢复重新连接。",
          confirmText: "离开",
        }))
      )
        return;
      try {
        $("body-error").hidden = true;
        const result = await apiPost("/api/body/control", { action });
        toast(
          action === "resume" && !result.ack.paused
            ? "已恢复，可以发起新任务"
            : "身体已暂停",
        );
        await refresh();
      } catch (e) {
        showError(e);
      }
    });
boot({ active: "body" }).then(async (user) => {
  if (!user?.admin) {
    $("body-status").textContent = "需要管理员权限";
    document
      .querySelectorAll(".body-buttons button")
      .forEach((b) => (b.disabled = true));
    return;
  }
  await refresh();
  stream = new EventSource("/api/body/events");
  stream.addEventListener("body", (e) => {
    try {
      render(JSON.parse(e.data));
    } catch {}
  });
  stream.onerror = () => {
    $("body-hint").textContent = "实时连接中断，正在重试。";
  };
  setInterval(refresh, 5000);
});
window.addEventListener("pagehide", () => stream?.close());
