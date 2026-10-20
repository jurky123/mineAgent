import { api, apiGet } from "./shell.js";
import { passwordForm } from "./passwords.js";
import { h, toast, withPending, confirmDialog } from "./ds.js";

const errors = {
  not_operator: "需要主站已验证的管理员账号；内容服务登录不授予身体权限。",
  body_not_found: "此玩家未接入身体。",
  body_paused: "身体已暂停。",
  stale_approval: "审批已过期或目标状态已改变，请重新发起。",
  stop_unconfirmed: "停止尚未得到确认，请核查最新状态。",
  body_not_ready: "身体尚未准备好。",
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

export async function mountBodyPanel(root, { agentId } = {}) {
  if (!/^[a-zA-Z0-9_-]{1,40}$/.test(agentId || ""))
    throw new Error("玩家 ID 无效");
  root.classList.add("body-player-panel");
  root.innerHTML = BODY_MARKUP;
  // Keep current state and tasks ahead of the infrequent game-pairing form.
  root
    .querySelector(".body-task-panel")
    .after(root.querySelector('[aria-label="游戏内控制授权"]'));
  const $ = (id) => root.querySelector("#" + id),
    privatePanel = root.querySelector(".body-private"),
    notice = root.querySelector(".body-access");
  const endpoint = (path) =>
    path +
    (path.includes("?") ? "&" : "?") +
    "agent=" +
    encodeURIComponent(agentId);
  const get = (path) =>
    api(endpoint(path), { signal: AbortSignal.timeout(12000) });
  const post = (path, data) =>
    api(endpoint(path), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data || {}),
      signal: AbortSignal.timeout(12000),
    });
  let stream,
    timer,
    staleTimer,
    refreshing = false,
    authorized = false,
    disposed = false,
    latest = null,
    user = null,
    conv = null,
    runBusy = false;
  const pairingKey = "mineagent.body-pair." + agentId;
  let conversationKey = "";
  const pending = async (button, fn) => {
    try {
      await withPending(button, fn);
    } finally {
      if (button.isConnected && !document.querySelector(".dialog-mask"))
        button.focus({ preventScroll: true });
    }
  };
  function captureCode() {
    const query = new URLSearchParams(location.hash.slice(1));
    const code = query.get("game-code");
    if (code !== null) {
      if (/^[0-9a-f]{32}$/.test(code)) {
        $("body-game-code").value = code;
        try {
          sessionStorage.setItem(
            pairingKey,
            JSON.stringify({ code, at: Date.now() }),
          );
        } catch {}
      }
      query.delete("game-code");
      history.replaceState(
        null,
        "",
        location.pathname +
          location.search +
          (query.size ? "#" + query.toString() : ""),
      );
    }
  }
  try {
    const saved = JSON.parse(sessionStorage.getItem(pairingKey) || "null");
    if (
      saved &&
      Date.now() - saved.at < 120000 &&
      /^[0-9a-f]{32}$/.test(saved.code)
    )
      $("body-game-code").value = saved.code;
    else sessionStorage.removeItem(pairingKey);
  } catch {}
  captureCode();
  window.addEventListener("hashchange", captureCode);
  function error(e) {
    if (!authorized || !$("body-error")) return;
    $("body-error").hidden = false;
    $("body-error").textContent =
      errors[e.message] || e.message || "未取得确认，请检查最新状态。";
  }
  function unknown() {
    if (!authorized) return;
    $("body-status").textContent = "状态未知";
    $("body-hint").textContent = "连接中断或状态过期，正在重新核查。";
    $("body-resume").disabled = true;
    $("body-task-send").disabled = true;
    $("body-position").textContent = "—";
    $("body-task").textContent = "未知";
    $("body-health").textContent = "—";
    $("body-item").textContent = "—";
  }
  function stopStreams() {
    stream?.close();
    stream = null;
    clearInterval(timer);
    clearInterval(staleTimer);
  }
  function deny(text, login = false) {
    authorized = false;
    stopStreams();
    privatePanel.replaceChildren();
    privatePanel.hidden = true;
    notice.hidden = false;
    notice.replaceChildren(h("p", "field-hint", text));
    if (login) {
      const details = h("details", "body-login");
      details.append(
        h("summary", null, "登录主站账号"),
        passwordForm(() => location.reload()),
      );
      notice.append(details);
    }
    const retry = h("button", "btn", "重新检查权限");
    retry.onclick = () => location.reload();
    notice.append(retry);
  }
  function render(st) {
    if (st.bodyId !== agentId) {
      deny("所选玩家与身体不匹配，已停止显示控制内容。");
      return false;
    }
    latest = st;
    const s = st.state || {},
      stale =
        st.stale ||
        !st.controlConnected ||
        !st.receivedAt ||
        Date.now() - st.receivedAt > 10000;
    $("body-name").textContent = st.name || agentId;
    $("body-status").textContent = stale
      ? "状态未知"
      : !s.connected
        ? "玩家离线"
        : st.draining
          ? "正在准备更新"
          : st.paused
            ? "已暂停"
            : s.task
              ? "正在行动"
              : "在线待机";
    $("body-hint").textContent = stale
      ? "当前状态可能过期。"
      : st.mode === "observe"
        ? "观察模式：可查询状态。"
        : st.paused
          ? "恢复后请重新发起任务，旧任务不会继续。"
          : "高危动作需要在下方明确审批。";
    $("body-task").textContent = stale
      ? "未知"
      : s.task
        ? labels[s.task.tool] || "执行动作"
        : "待机";
    $("body-health").textContent =
      !stale && s.connected ? `${s.health} / ${s.food}` : "—";
    $("body-item").textContent =
      !stale && s.connected ? s.heldItem || "空手" : "—";
    $("body-position").textContent =
      !stale && s.connected
        ? `${s.worldId || s.dimension || "世界未知"} · ${(s.pos || []).map((n) => Number(n).toFixed(1)).join(", ")}`
        : "—";
    $("body-updated").textContent =
      st.receivedAt > 0 ? new Date(st.receivedAt).toLocaleString() : "尚无状态";
    $("body-resume").disabled = !st.ready || stale || !!st.busy;
    $("body-task-send").disabled = stale || runBusy;
    $("body-task-stop").disabled = !runBusy;
    const list = $("body-events");
    list.replaceChildren();
    for (const e of (st.events || []).slice(-12).reverse()) {
      const row = h("li");
      row.append(
        h("time", null, new Date(e.at).toLocaleTimeString()),
        document.createTextNode(labels[e.type] || "状态更新"),
      );
      if (e.type === "chat")
        row.append(
          h(
            "span",
            null,
            ` · ${e.data?.player || "玩家"}：${e.data?.message || ""}`,
          ),
        );
      list.append(row);
    }
    if (!list.children.length) list.append(h("li", null, "暂无事件"));
    return true;
  }
  async function approvals() {
    const data = await get("/api/body/approvals");
    const list = $("body-approvals");
    if (disposed || !authorized) return;
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
          pending(b, async () => {
            try {
              await post(
                "/api/body/approvals/" + encodeURIComponent(a.approvalId),
                { approved },
              );
              toast(approved ? "已批准，等待实际执行结果" : "已拒绝");
              await refresh();
            } catch (e) {
              error(e);
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
  async function replies() {
    if (!conv || !authorized) return;
    const data = await get(
      "/api/body/task/history?conv=" + encodeURIComponent(conv),
    );
    if (disposed || !authorized) return;
    runBusy = !!data.running;
    $("body-run-status").textContent =
      data.running?.text || (runBusy ? "正在处理…" : "");
    const list = $("body-replies");
    list.replaceChildren();
    for (const msg of (data.messages || []).slice(-12)) {
      const row = h("li", "body-reply");
      row.append(
        h("strong", null, msg.role === "agent" ? "AI 回复" : "我的任务"),
        h("p", null, msg.text || ""),
      );
      list.append(row);
    }
    if (latest) render(latest);
  }
  async function refresh() {
    if (refreshing || disposed || !authorized) return;
    refreshing = true;
    try {
      const data = await get("/api/body/status");
      if (!render(data)) return;
      await Promise.all([approvals(), replies()]);
      $("body-error").hidden = true;
    } catch (e) {
      if (e.message === "not_operator" || e.unauthorized) {
        deny(errors.not_operator, true);
      } else {
        unknown();
        error(e);
      }
    } finally {
      refreshing = false;
    }
  }
  $("body-game-link").onsubmit = (e) => {
    e.preventDefault();
    pending($("body-game-approve"), async () => {
      const code = $("body-game-code").value.trim();
      if (!/^[0-9a-f]{32}$/.test(code)) {
        error(new Error("请输入游戏面板中的 32 位临时代码。"));
        return;
      }
      try {
        const p = await post("/api/body/minecraft/link", {
          action: "preview",
          code,
        });
        if (
          !(await confirmDialog({
            title: "授权当前游戏连接",
            body: `玩家：${p.player}\nUUID：${p.uuid}\n仅授权这一连接，最长 10 分钟。请核对这是你当前的游戏连接。`,
            confirmText: "授权此连接",
          }))
        )
          return;
        await post("/api/body/minecraft/link", {
          action: "approve",
          code,
          uuid: p.uuid,
          session: p.session,
        });
        $("body-game-code").value = "";
        try {
          sessionStorage.removeItem(pairingKey);
        } catch {}
        $("body-game-result").textContent =
          "已授权。回到游戏面板刷新，离线或到期后失效。";
      } catch (e) {
        $("body-game-result").textContent =
          "授权未确认，请核查代码与游戏连接后重试。";
        error(e);
      }
    });
  };
  $("body-game-revoke").onclick = () =>
    pending($("body-game-revoke"), async () => {
      try {
        await post("/api/body/minecraft/link", { action: "revoke" });
        $("body-game-result").textContent =
          "已撤销本次网页登录授予的游戏控制。";
      } catch (e) {
        error(e);
      }
    });
  for (const [id, action] of [
    ["body-stop", "stop"],
    ["body-resume", "resume"],
    ["body-disconnect", "disconnect"],
  ])
    $(id).onclick = async () => {
      if (
        action === "disconnect" &&
        !(await confirmDialog({
          title: "离开游戏",
          body: "该 AI 玩家会离开游戏。",
          confirmText: "离开",
        }))
      )
        return;
      pending($(id), async () => {
        try {
          const result = await post("/api/body/control", { action });
          const ack = result.ack;
          if (ack?.paused === true) toast("已确认暂停");
          else if (action === "resume" && ack?.paused === false)
            toast("已确认恢复，可以发起新任务");
          else error(new Error("操作已提交，尚未确认最新状态。"));
          await refresh();
        } catch (e) {
          error(e);
        }
      });
    };
  $("body-task-form").onsubmit = (e) => {
    e.preventDefault();
    const text = $("body-task-input").value.trim();
    if (!text || runBusy) return;
    pending($("body-task-send"), async () => {
      try {
        const payload = { text };
        if (conv) payload.conv = conv;
        const data = await post("/api/body/task/send", payload);
        conv = data.conv;
        try {
          localStorage.setItem(conversationKey, conv);
        } catch {}
        $("body-agent-chat").href = "/agent?conv=" + encodeURIComponent(conv);
        $("body-task-input").value = "";
        runBusy = !!data.running;
        $("body-run-status").textContent = "已提交，等待实际回复。";
        await replies();
      } catch (e) {
        error(
          new Error(
            "任务提交未确认；输入已保留，请先在 Agent 核对结果，勿直接重复发送。",
          ),
        );
      }
    });
  };
  $("body-task-stop").onclick = () =>
    pending($("body-task-stop"), async () => {
      try {
        if (conv) await post("/api/body/task/stop", { conv });
        await replies();
      } catch (e) {
        error(e);
      }
    });
  $("body-task-new").onclick = () => {
    if (runBusy) {
      error(new Error("请先停止或等待当前任务完成。"));
      return;
    }
    conv = null;
    try {
      localStorage.removeItem(conversationKey);
    } catch {}
    $("body-replies").replaceChildren();
    $("body-run-status").textContent = "新任务会话，发送前不会创建记录。";
    $("body-agent-chat").href = "/agent";
  };
  const dispose = () => {
    disposed = true;
    stopStreams();
    window.removeEventListener("hashchange", captureCode);
  };
  window.addEventListener("pagehide", dispose, { once: true });
  try {
    user = await apiGet("/api/auth/me");
    if (!user.admin) {
      deny("身体详情和控制仅对主站管理员开放。");
      return dispose;
    }
    const snapshot = await get("/api/body/status");
    authorized = true;
    if (!render(snapshot)) return dispose;
    conversationKey = `mineagent.body-conv.${user.id}.${agentId}`;
    try {
      const saved = localStorage.getItem(conversationKey);
      if (/^[a-zA-Z0-9_-]{6,32}$/.test(saved || "")) conv = saved;
    } catch {}
    if (conv)
      $("body-agent-chat").href = "/agent?conv=" + encodeURIComponent(conv);
    notice.hidden = true;
    privatePanel.hidden = false;
    await refresh();
    if (!authorized) return dispose;
    stream = new EventSource(endpoint("/api/body/events"));
    stream.addEventListener("body", (e) => {
      try {
        if (authorized && !disposed) render(JSON.parse(e.data));
      } catch {
        unknown();
      }
    });
    stream.onerror = unknown;
    timer = setInterval(refresh, 5000);
    staleTimer = setInterval(() => {
      if (latest && Date.now() - latest.receivedAt > 10000) unknown();
    }, 1000);
  } catch (e) {
    deny(
      e.message === "body_not_found"
        ? errors.body_not_found
        : errors.not_operator,
      true,
    );
  }
  return dispose;
}

const BODY_MARKUP =
  '<div class="body-access" role="status"><p>正在验证主站权限…</p></div><div class="body-private" hidden>  <section class="body-control" aria-label="身体控制">\n    <div><strong id="body-status" role="status">正在连接…</strong><p id="body-hint" class="field-hint">正在读取最新状态</p></div>\n    <div class="body-buttons"><button class="btn danger" id="body-stop">停止并暂停</button><button class="btn primary" id="body-resume">恢复</button><button class="btn" id="body-disconnect">离开游戏</button><a class="btn" id="body-agent-chat" href="/agent">打开 Agent</a></div>\n  </section>\n  <p id="body-error" class="body-error" role="alert" hidden></p>\n  <section class="body-card" aria-label="游戏内控制授权">\n    <h2>游戏内控制授权</h2>\n    <p class="field-hint">在游戏内 AI 玩家面板获取临时代码，核对玩家与连接后授权。授权最长 10 分钟，离线后失效；游戏内 OP 不会自动获得网页管理员身份。</p>\n    <form id="body-game-link" class="body-buttons">\n      <label for="body-game-code">临时代码</label><input id="body-game-code" name="code" maxlength="32" minlength="32" autocomplete="off" spellcheck="false" required>\n      <button type="submit" class="btn primary" id="body-game-approve">核对并授权</button>\n      <button type="button" class="btn" id="body-game-revoke">撤销本次网页登录授予的游戏控制</button>\n    </form>\n    <p id="body-game-result" role="status" class="field-hint"></p>\n  </section>\n  <section class="body-grid" aria-label="玩家状态">\n    <article class="body-card"><h2 id="body-name">AI 玩家</h2><dl><dt>当前活动</dt><dd id="body-task">—</dd><dt>生命 / 饥饿</dt><dd id="body-health">—</dd><dt>手持</dt><dd id="body-item">—</dd><dt>位置</dt><dd id="body-position">—</dd><dt>状态更新</dt><dd id="body-updated">—</dd></dl></article>\n    <article class="body-card"><h2>待审核动作 <span class="badge" id="body-pending-count">0</span></h2><p class="field-hint">批准仅适用于这一项具体动作。目标或控制状态变化后，需要重新发起。</p><div id="body-approvals" aria-live="polite"></div></article>\n  </section>\n<section class="body-card body-task-panel" aria-label="任务与回复"><h2>任务与回复</h2><p class="field-hint">仅向当前 AI 玩家提交明确任务。沿用 Agent 的会话、权限与审批；提交不代表执行完成。</p><ol id="body-replies" class="body-replies" aria-live="polite"></ol><p id="body-run-status" role="status" class="field-hint"></p><form id="body-task-form"><label for="body-task-input">给当前玩家的任务</label><textarea class="input" id="body-task-input" maxlength="4000" rows="3" required></textarea><div class="body-buttons"><button class="btn primary" type="submit" id="body-task-send">发送任务</button><button class="btn" type="button" id="body-task-stop">停止本次任务</button><button class="btn" type="button" id="body-task-new">新任务会话</button></div></form></section>\n  <section class="body-card"><h2>最近事件</h2><ul id="body-events" class="body-events"></ul></section>\n</div>';
