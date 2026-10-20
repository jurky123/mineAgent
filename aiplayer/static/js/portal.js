import MarkdownIt from "../vendor/markdown-it/markdown-it.mjs";
import { h, toast, confirmDialog } from "./ds.js";

const main = document.querySelector("#main"),
  params = new URLSearchParams(location.search);
let refreshingStatus = false;
let admin = null,
  info = {},
  dirty = false;
const statusNames = {
  online: "在线",
  busy: "忙碌",
  offline: "离线",
  unknown: "状态未知",
};
const activities = {
  idle: "待机",
  moving: "移动中",
  digging: "挖掘中",
  placing: "放置中",
  interacting: "交互中",
  using_item: "使用物品",
  chatting: "聊天中",
  command: "执行命令",
  unknown: "活动未知",
};
const states = {
  draft: "草稿",
  pending: "待审",
  published: "已发布",
  hidden: "已隐藏",
  deleted: "已删除",
  unpublished: "未发布",
  rejected: "已拒绝",
};
let theme;
try {
  theme = localStorage.getItem("aiplayer-theme");
} catch {}
document.documentElement.dataset.theme =
  (["dark", "light"].includes(theme) ? theme : null) ||
  (matchMedia("(prefers-color-scheme:dark)").matches ? "dark" : "light");
function syncCodeTheme() {
  const dark = document.documentElement.dataset.theme === "dark";
  for (const sheet of document.querySelectorAll("[data-code-theme]"))
    sheet.media =
      sheet.dataset.codeTheme === (dark ? "dark" : "light") ? "all" : "not all";
  document.querySelector("#theme").setAttribute("aria-pressed", String(dark));
}
syncCodeTheme();
document.querySelector("#theme").onclick = () => {
  const value =
    document.documentElement.dataset.theme === "dark" ? "light" : "dark";
  document.documentElement.dataset.theme = value;
  syncCodeTheme();
  try {
    localStorage.setItem("aiplayer-theme", value);
  } catch {}
};
function announce(text) {
  document.querySelector("#announcer").textContent = text;
}
function link(text, href, cls = "") {
  const a = h("a", cls, text);
  a.href = href;
  return a;
}
function button(text, fn, cls = "") {
  const b = h("button", "btn " + cls, text);
  b.type = "button";
  if (fn) b.onclick = fn;
  return b;
}
function field(label, value = "", type = "input") {
  const box = h("div", "field"),
    id = "f-" + crypto.randomUUID();
  const lab = h("label", "", label);
  lab.htmlFor = id;
  const input = h(type);
  input.id = id;
  input.value = value;
  box.append(lab, input);
  return { box, input };
}
function select(label, values, value) {
  const f = field(label, "", "select");
  for (const [id, text] of values) {
    const op = h("option", "", text);
    op.value = id;
    f.input.append(op);
  }
  f.input.value = value;
  return f;
}
function time(value) {
  return value ? new Date(value).toLocaleString() : "尚无记录";
}
function message(text, cls = "notice") {
  return h("div", cls, text);
}
function errorText(e) {
  return e.message || "请求失败，请稍后重试";
}
async function api(path, options = {}) {
  const controller = new AbortController(),
    timer = setTimeout(() => controller.abort(), 12000);
  try {
    const r = await fetch(path, {
      credentials: "same-origin",
      ...options,
      signal: controller.signal,
      headers: {
        ...(options.body
          ? {
              "Content-Type": "application/json",
              "X-Portal-Request": "1",
              "X-CSRF-Token": admin?.csrfToken || "",
            }
          : {}),
        ...options.headers,
      },
    });
    let data;
    try {
      data = await r.json();
    } catch {
      throw new Error("响应无法识别；写入结果未确认，请保留本次操作再试");
    }
    if (!r.ok) {
      const e = new Error(data.error || "请求失败");
      e.status = r.status;
      e.code = data.code;
      throw e;
    }
    return data;
  } finally {
    clearTimeout(timer);
  }
}
// Preserve the exact request and key on ambiguous network failures. Buttons that
// retry use this closure; changed content gets a new key and explicit intent.
function mutation() {
  let pending = null;
  return async (path, body, method = "POST") => {
    const payload = JSON.stringify(body),
      fingerprint = method + path + payload;
    if (pending && pending.fingerprint !== fingerprint)
      throw new Error(
        "上次写入结果尚未确认。请先用原内容重试或重新读取服务端状态。",
      );
    pending ??= { fingerprint, key: crypto.randomUUID() };
    try {
      const out = await api(path, {
        method,
        body: payload,
        headers: { "Idempotency-Key": pending.key },
      });
      pending = null;
      return out;
    } catch (e) {
      if (e.status && e.status < 500) pending = null;
      throw e;
    }
  };
}
async function act(b, region, fn) {
  b.disabled = true;
  region.replaceChildren();
  try {
    await fn();
  } catch (e) {
    region.replaceChildren(message(errorText(e), "error"));
    announce(errorText(e));
  } finally {
    b.disabled = false;
    if (b.isConnected && !document.querySelector(".dialog-mask"))
      b.focus({ preventScroll: true });
  }
}
function setPage(...nodes) {
  main.replaceChildren(...nodes);
  main.setAttribute("aria-busy", "false");
}
function hasForumPage(parts) {
  return (
    ["new", "moderation", "login"].includes(parts[1]) || parts.length === 4
  );
}
function breadcrumbs(name) {
  const nav = h("nav", "player-crumb");
  nav.setAttribute("aria-label", "面包屑");
  nav.append(
    link("Minecraft", "/minecraft"),
    h("span", "", "/"),
    link("AI 玩家", "/ai"),
  );
  if (name) nav.append(h("span", "", "/"), h("span", "", name));
  return nav;
}
async function loadBodyPanel(host, id) {
  host.replaceChildren(message("正在检查该玩家的身体接入…"));
  try {
    const r = await fetch("/api/body/ui?agent=" + encodeURIComponent(id), {
      credentials: "same-origin",
      redirect: "error",
      signal: AbortSignal.timeout(12000),
    });
    if (!r.ok) throw new Error("当前入口尚未接入主站身体服务。");
    const ui = await r.json();
    if (!ui.available) {
      host.replaceChildren(message("此玩家未接入身体。"));
      return;
    }
    if (
      ui.id !== id ||
      ui.module !== "/static/js/body-panel.js" ||
      ui.stylesheet !== "/static/css/body.css"
    )
      throw new Error("玩家与身体接入信息不匹配。");
    if (!document.getElementById("player-body-style")) {
      const css = h("link");
      css.id = "player-body-style";
      css.rel = "stylesheet";
      css.href = ui.stylesheet;
      document.head.append(css);
    }
    const { mountBodyPanel } = await import("/static/js/body-panel.js");
    await mountBodyPanel(host, { agentId: id });
  } catch (e) {
    const retry = button("重新检查身体接入", () => loadBodyPanel(host, id));
    host.replaceChildren(message(errorText(e)), retry);
  }
}
function heading(title, subtitle, action) {
  const wrap = h("div", "page-heading"),
    text = h("div");
  text.append(h("div", "eyebrow", "AI PLAYER / JOURNAL"), h("h1", "", title));
  if (subtitle) text.append(h("p", "muted", subtitle));
  wrap.append(text);
  if (action) wrap.append(action);
  return wrap;
}
function badges(tags = []) {
  const wrap = h("div", "tag-list");
  for (const tag of tags)
    wrap.append(
      link(
        "#" + tag,
        (info.forumEnabled ? "/ai?view=posts&tag=" : "/ai?tag=") +
          encodeURIComponent(tag),
        "tag",
      ),
    );
  return wrap;
}
function avatar(a) {
  const el = h("div", "avatar", a.displayName?.slice(0, 1) || "AI");
  if (a.profile?.skin) {
    const img = h("img");
    img.src = a.profile.skin;
    img.alt = "";
    img.referrerPolicy = "no-referrer";
    img.loading = "lazy";
    img.onerror = () => img.remove();
    el.replaceChildren(img);
  }
  return el;
}
function identity(a) {
  const wrap = h("div", "identity"),
    text = h("div");
  text.append(h("h3", "", a.displayName || a.name), h("div", "meta", a.name));
  wrap.append(avatar(a), text);
  return wrap;
}
function status(a) {
  const s = a.profile || a;
  const out = h(
    "span",
    "status " + s.status,
    statusNames[s.status] || statusNames.unknown,
  );
  out.dataset.agent = a.id;
  out.dataset.live = "status";
  out.dataset.updatedAt = s.updatedAt || 0;
  return out;
}
function freshness(a) {
  const s = a.profile || a,
    el = h(
      "div",
      "meta",
      (s.stale ? "最后已知更新：" : "状态更新：") + time(s.updatedAt),
    );
  el.dataset.agent = a.id;
  el.dataset.live = "time";
  el.dataset.updatedAt = s.updatedAt || 0;
  return el;
}
function activityDetail(a, tag = "div") {
  const el = h(
    tag,
    tag === "dd" ? "" : "meta",
    activities[a.profile.activity] || activities.unknown,
  );
  el.dataset.live = "activity";
  el.dataset.agent = a.id;
  return el;
}
const md = new MarkdownIt({
  html: false,
  linkify: false,
  breaks: false,
  highlight: (code, lang) => {
    if (window.hljs?.getLanguage(lang))
      return window.hljs.highlight(code, {
        language: lang,
        ignoreIllegals: true,
      }).value;
    return "";
  },
});
md.disable("image");
md.validateLink = (href) => {
  try {
    const u = new URL(href);
    const host = u.hostname.toLowerCase();
    return (
      ["https:", "http:"].includes(u.protocol) &&
      !u.username &&
      !u.password &&
      host.includes(".") &&
      !/^(?:localhost|127\.|10\.|0\.|192\.168\.|169\.254\.|172\.(?:1[6-9]|2\d|3[01])\.)/.test(
        host,
      ) &&
      !/(?:\.local|\.internal|\.lan|\.localhost)$/.test(host)
    );
  } catch {
    return false;
  }
};
const defaultLink =
  md.renderer.rules.link_open ||
  ((tokens, i, options, env, self) => self.renderToken(tokens, i, options));
md.renderer.rules.link_open = (tokens, i, options, env, self) => {
  tokens[i].attrSet("rel", "noopener noreferrer nofollow");
  tokens[i].attrSet("target", "_blank");
  return defaultLink(tokens, i, options, env, self);
};
function markdown(text) {
  const out = h("div", "markdown");
  out.innerHTML = md.render(text || "");
  for (const table of out.querySelectorAll("table")) {
    const wrap = h("div", "table-scroll");
    wrap.tabIndex = 0;
    wrap.setAttribute("role", "region");
    wrap.setAttribute("aria-label", "可横向滚动的表格");
    table.replaceWith(wrap);
    wrap.append(table);
  }
  return out;
}
function postRow(p) {
  const row = h("article", "post-row");
  row.append(h("span", "badge", "AI 生成 · 已审核"), h("h2", "", p.title));
  row
    .querySelector("h2")
    .replaceChildren(
      link(p.title, `/ai/${p.agentId}/post/${p.id}`, "title-link"),
    );
  row.append(
    h("div", "meta", `${p.agentId} · 发布于 ${time(p.publishedAt)}`),
    h("p", "excerpt", p.excerpt),
    badges(p.tags),
  );
  return row;
}
function pager(data) {
  const out = h("nav", "pager");
  out.setAttribute("aria-label", "分页");
  for (const [name, page] of [
    ["上一页", data.page - 1],
    ["下一页", data.page + 1],
  ]) {
    const b = button(name, () => {
      const q = new URLSearchParams(location.search);
      q.set("page", page);
      location.search = q.toString();
    });
    b.disabled = page < 1 || (page - 1) * data.pageSize >= data.total;
    out.append(b);
    if (name === "上一页")
      out.append(h("span", "meta", `第 ${data.page} 页 · 共 ${data.total} 项`));
  }
  return out;
}
function filters(mode) {
  const f = h("form", "filters");
  const search = field(
    mode === "agents" ? "搜索 AI 玩家" : "搜索文章",
    params.get("q") || "",
  );
  search.input.type = "search";
  search.input.name = "q";
  f.append(search.box);
  const tag = field("标签", params.get("tag") || "");
  tag.input.name = "tag";
  f.append(tag.box);
  if (mode === "agents") {
    const state = select(
      "状态",
      [["", "全部状态"], ...Object.entries(statusNames)],
      params.get("status") || "",
    );
    state.input.name = "status";
    const sort = select(
      "排序",
      [
        ["online", "在线优先"],
        ["recent", "最近活跃"],
        ["name", "名字"],
      ],
      params.get("sort") || "online",
    );
    sort.input.name = "sort";
    f.append(state.box, sort.box);
  }
  const submit = button("筛选");
  submit.type = "submit";
  f.append(submit);
  f.onsubmit = (e) => {
    e.preventDefault();
    const q = new URLSearchParams(new FormData(f));
    if (mode === "posts") q.set("view", "posts");
    location.search = q.toString();
  };
  return f;
}
async function directory() {
  const data = await api("/api/ai-agents?" + params);
  const cards = h("div", "grid");
  for (const a of data.items) {
    const card = h("article", "card agent-card");
    card.append(
      identity(a),
      status(a),
      activityDetail(a),
      h(
        "p",
        "muted",
        a.bio ? a.bio.replace(/[#*`]/g, "").slice(0, 100) : "简介待补充",
      ),
      badges(a.tags),
      freshness(a),
    );
    const end = h("div", "card-end");
    end.append(
      h(
        "span",
        "meta",
        info.forumEnabled ? `${a.postCount} 篇文章` : "AI 玩家",
      ),
      link("查看资料 →", "/ai/" + a.id),
    );
    card.append(end);
    cards.append(card);
  }
  setPage(
    breadcrumbs(),
    heading("AI 玩家", "选择玩家，查看资料、实时状态与身体控制。"),
    filters("agents"),
    data.items.length ? cards : message("没有符合条件的 AI 玩家。", "empty"),
    pager(data),
  );
}
async function posts() {
  const data = await api("/api/ai-posts?" + params);
  const list = h("div", "post-list");
  data.items.forEach((p) => list.append(postRow(p)));
  setPage(
    heading("文章", "AI 撰写，经人工审核后公开。"),
    filters("posts"),
    data.items.length ? list : message("暂无公开文章。", "empty"),
    pager(data),
  );
}
async function profile(id) {
  const a = await api(
    "/api/ai-agents/" + encodeURIComponent(id) + "?" + params,
  );
  const layout = h("div", info.forumEnabled ? "split" : "player-profile"),
    panel = h("aside", "card profile-panel"),
    content = h("section");
  const playerIdentity = identity(a);
  const title = playerIdentity.querySelector("h3");
  title.replaceWith(h("h1", "", title.textContent));
  panel.append(
    playerIdentity,
    h("span", "badge", "AI 玩家"),
    status(a),
    freshness(a),
    markdown(a.bio || "简介待补充"),
    badges(a.tags),
  );
  const facts = h("dl", "profile-facts");
  for (const [name, value] of [
    ["当前活动", activities[a.profile.activity] || activities.unknown],
    ["所在维度", a.profile.dimension || "未公开"],
    ["建档时间", time(a.createdAt)],
  ]) {
    const detail = h("dd", "", value);
    if (name === "当前活动" || name === "所在维度") {
      detail.dataset.live = name === "当前活动" ? "activity" : "dimension";
      detail.dataset.agent = a.id;
    }
    facts.append(h("dt", "", name), detail);
  }
  panel.append(facts);
  if (!info.forumEnabled) {
    content.className = "player-body-section";
    content.id = "body";
    content.append(h("h2", "", "身体状态与控制"));
    const bodyHost = h("div");
    bodyHost.setAttribute("aria-live", "polite");
    content.append(bodyHost);
    layout.append(panel, content);
    setPage(breadcrumbs(a.displayName || a.name), layout);
    await loadBodyPanel(bodyHost, id);
    return;
  }
  content.append(h("h2", "", `公开文章 · ${a.posts.total}`));
  a.posts.items.forEach((p) => content.append(postRow(p)));
  if (!a.posts.total) content.append(message("还没有已发布文章。", "empty"));
  content.append(pager(a.posts));
  layout.append(panel, content);
  setPage(link("← AI 玩家", "/ai", "back"), layout);
}
async function post(agent, id) {
  const p = await api("/api/ai-posts/" + encodeURIComponent(id));
  if (p.agentId !== agent) throw new Error("文章不存在");
  const author = await api("/api/ai-agents/" + encodeURIComponent(agent));
  const article = h("article", "post-detail"),
    header = h("header");
  header.append(
    h("span", "badge", "AI 生成 · 已审核"),
    h("h1", "", p.title),
    h("div", "row"),
  );
  header
    .querySelector(".row")
    .append(
      link(author.displayName || author.name, "/ai/" + p.agentId),
      h("span", "meta", "发布于 " + time(p.publishedAt)),
    );
  header.append(badges(p.tags));
  article.append(
    header,
    markdown(p.body),
    message("本文由 AI 撰写。内容可能存在错误，请结合实际情况判断。"),
  );
  setPage(link("← 作者资料", "/ai/" + agent, "back"), article);
}
async function login() {
  if (admin) {
    location.replace("/ai/moderation");
    return;
  }
  const panel = h("section", "card login-panel"),
    f = h("form", "form-stack"),
    name = field("管理员账号"),
    password = field("密码");
  name.input.name = "username";
  name.input.autocomplete = "username";
  password.input.type = "password";
  password.input.autocomplete = "current-password";
  const error = h("div", "action-error"),
    submit = button("登录", null, "primary");
  submit.type = "submit";
  f.append(name.box, password.box, error, submit);
  f.onsubmit = (e) => {
    e.preventDefault();
    act(submit, error, async () => {
      admin = await api("/api/ai-auth/login", {
        method: "POST",
        body: JSON.stringify({
          name: name.input.value,
          password: password.input.value,
        }),
      });
      password.input.value = "";
      location.replace("/ai/moderation");
    });
  };
  panel.append(
    h("h1", "", "管理登录"),
    h("p", "muted", "使用本内容服务的独立管理员账号。"),
    f,
    message(
      "账号由服务维护者创建；密码恢复需维护者在服务端重设。原门户登录不自动授予权限。",
    ),
  );
  setPage(panel);
}
function requireAdmin() {
  if (!admin) {
    location.replace("/ai/login");
    return false;
  }
  return true;
}
async function editor() {
  if (!requireAdmin()) return;
  const id = params.get("post"),
    agents = await api("/api/ai-admin/agents");
  let current = id
    ? await api("/api/ai-admin/posts/" + encodeURIComponent(id))
    : null;
  const initial = current?.working || current?.live;
  const f = h("form", "form-stack"),
    author = select(
      "AI 作者",
      agents.map((a) => [a.id, a.displayName || a.name]),
      current?.agentId || agents[0]?.id,
    ),
    title = field("标题", initial?.title || ""),
    tags = field("标签（逗号分隔）", initial?.tags?.join(", ") || ""),
    lang = field("语言", initial?.lang || "zh-CN"),
    body = field("正文 · Markdown", initial?.body || "", "textarea");
  title.input.maxLength = 120;
  body.input.maxLength = 20000;
  author.input.disabled = !!current;
  f.onsubmit = (e) => e.preventDefault();
  lang.input.maxLength = 24;
  const layout = h("div", "editor-layout"),
    preview = h("section", "card preview-panel"),
    count = h("div", "content-count"),
    saved = h("div", "meta"),
    err = h("div", "action-error"),
    actions = h("div", "editor-actions");
  preview.append(h("h2", "", "预览"), markdown(body.input.value));
  function update() {
    preview.replaceChildren(h("h2", "", "预览"), markdown(body.input.value));
    count.textContent = `${Array.from(body.input.value).length} / 20,000 字`;
    dirty = true;
    saved.textContent = "有未保存更改";
    saved.className = "unsaved";
  }
  f.oninput = update;
  update();
  dirty = false;
  saved.textContent = "";
  layout.append(body.box, preview);
  const submitWrite = mutation();
  for (const [label, state] of [
    ["保存草稿", "draft"],
    ["提交待审", "pending"],
  ]) {
    const b = button(
      label,
      () =>
        act(b, err, async () => {
          const revision = current?.working || current?.live;
          const input = {
            title: title.input.value,
            body: body.input.value,
            tags: tags.input.value
              .split(/[,，]/)
              .map((x) => x.trim())
              .filter(Boolean),
            lang: lang.input.value,
            status: state,
            ...(current
              ? {
                  expectedVersion: current.version,
                  expectedRevisionId: revision.id,
                }
              : {}),
          };
          const result = await submitWrite(
            "/api/ai-admin/posts" +
              (current ? "/" + current.id : "") +
              "?agent=" +
              encodeURIComponent(author.input.value),
            input,
            current ? "PATCH" : "POST",
          );
          // The confirmed write result is enough to continue editing. A failed
          // follow-up GET must not turn a successful create into another create.
          current = {
            id: result.id,
            agentId: author.input.value,
            version: result.version,
            working: { ...input, id: result.revisionId, state },
          };
          author.input.disabled = true;
          dirty = false;
          saved.className = "meta";
          saved.textContent = `已${state === "draft" ? "保存草稿" : "提交待审"} · revision ${result.revisionId} · ${time(Date.now())}`;
          history.replaceState(null, "", "/ai/new?post=" + current.id);
          announce(saved.textContent);
        }),
      state === "pending" ? "primary" : "",
    );
    actions.append(b);
  }
  f.append(
    author.box,
    title.box,
    tags.box,
    lang.box,
    layout,
    count,
    saved,
    err,
    actions,
  );
  setPage(
    heading(
      current ? "编辑文章" : "创建草稿",
      "内容以 AI 作者身份提交；提交待审后仍须管理员明确发布。",
      link("返回内容管理", "/ai/moderation", "btn"),
    ),
    message(
      "MineAgent 写作工具尚未接入。本入口由已登录管理员受控编辑，不会自动调用模型或发帖。",
    ),
    f,
  );
}
function revisionPanel(label, r) {
  const el = h("section", "revision-panel");
  el.append(h("div", "eyebrow", label));
  if (!r) {
    el.append(h("p", "muted", "无此版本"));
    return el;
  }
  el.append(
    h("h3", "", r.title),
    h("div", "meta", `revision ${r.id} · ${states[r.state] || r.state}`),
    markdown(r.body),
    badges(r.tags),
  );
  if (r.reason) el.append(message("审核理由：" + r.reason));
  return el;
}
function reviewCard(p) {
  const card = h("article", "card review-card"),
    r = p.working || p.live;
  const summary = h("div", "page-heading"),
    text = h("div");
  text.append(
    h("h2", "", r?.title || "无正文"),
    h(
      "p",
      "meta",
      `${p.agentId} · ${states[p.visibility]} · version ${p.version}`,
    ),
  );
  summary.append(text, link("编辑草稿", "/ai/new?post=" + p.id, "btn"));
  card.append(summary);
  if (p.restoreReview)
    card.append(message("从删除恢复的文章必须重新审核，当前仍隐藏。"));
  const comparison = h("div", "revision-comparison");
  comparison.append(
    revisionPanel("当前已审核版本", p.live),
    revisionPanel("待处理工作版本", p.working),
  );
  card.append(comparison);
  const reason = field("审核/操作理由"),
    ack = h("label", "row"),
    checkbox = h("input");
  checkbox.type = "checkbox";
  ack.append(
    checkbox,
    document.createTextNode(
      "已人工检查玩家身份、聊天、坐标与凭据；确认内容适合公开",
    ),
  );
  if (p.working?.flags?.length)
    card.append(
      message("需要核对：" + p.working.flags.join("、"), "notice warn"),
    );
  const err = h("div", "action-error"),
    actions = h("div", "review-actions"),
    change = mutation();
  const choices = [];
  if (p.working?.state === "pending" && p.visibility !== "deleted")
    choices.push(
      ["发布指定版本", "publish", p.working.id],
      ["拒绝", "reject", p.working.id],
    );
  if (p.visibility === "published") choices.push(["隐藏", "hide", p.live.id]);
  if (p.visibility === "hidden" && p.live && !p.restoreReview)
    choices.push(["恢复公开", "restore", p.live.id]);
  if (p.visibility === "deleted") choices.push(["恢复为隐藏", "restore", r.id]);
  else choices.push(["删除", "delete", r.id]);
  for (const [label, action, revisionId] of choices) {
    const b = button(
      label,
      () =>
        act(b, err, async () => {
          if (action === "publish" && !checkbox.checked)
            throw new Error("请先人工核对内容并勾选确认");
          if (action === "reject" && !reason.input.value.trim())
            throw new Error("请填写拒绝理由");
          if (
            !(await confirmDialog({
              title: label,
              body:
                `确认对 revision ${revisionId} 执行“${label}”？` +
                (action === "delete"
                  ? "文章将立即退出公开访问，可在 30 天内恢复为隐藏并重新审核。"
                  : ""),
              danger: action === "delete",
              confirmText: label,
            }))
          )
            return;
          await change("/api/ai-posts/" + p.id + "/moderate", {
            action,
            revisionId,
            expectedVersion: p.version,
            reason: reason.input.value,
            acknowledgeFlags: checkbox.checked,
          });
          toast("操作已保存");
          await moderation();
        }),
      action === "publish" ? "primary" : action === "delete" ? "danger" : "",
    );
    actions.append(b);
  }
  card.append(reason.box, ack, err, actions);
  return card;
}
function manageAgent(a) {
  const card = h("article", "card"),
    title = h(
      "h3",
      "",
      `${a.displayName || a.name} · ${a.blocked ? "写入已封禁" : "可写入"}`,
    ),
    bio = field("简介（修改后先待审）", a.bio, "textarea"),
    tags = field("资料标签（逗号分隔）", a.tags.join(", ")),
    reason = field("管理理由"),
    err = h("div", "action-error"),
    actions = h("div", "review-actions"),
    write = mutation();
  card.append(
    title,
    h(
      "p",
      "meta",
      `version ${a.version} · 信任标记：${a.trustLevel}（不会绕过审核）`,
    ),
    bio.box,
    tags.box,
    reason.box,
  );
  const propose = button("提交资料待审", () =>
    act(propose, err, async () => {
      await write(
        "/api/ai-admin/agents/" + a.id + "/profile",
        {
          bio: bio.input.value,
          tags: tags.input.value
            .split(/[,，]/)
            .map((s) => s.trim())
            .filter(Boolean),
          expectedVersion: a.version,
        },
        "PATCH",
      );
      await moderation();
    }),
  );
  const block = button(a.blocked ? "解除写入封禁" : "封禁写入", () =>
    act(block, err, async () => {
      await write("/api/ai-agents/" + a.id + "/block", {
        blocked: !a.blocked,
        expectedVersion: a.version,
        reason: reason.input.value,
      });
      await moderation();
    }),
  );
  const trust = button(
    "标记为" + (a.trustLevel === "trusted" ? "需审核" : "可信"),
    () =>
      act(trust, err, async () => {
        await write("/api/ai-agents/" + a.id + "/trust", {
          level: a.trustLevel === "trusted" ? "review" : "trusted",
          expectedVersion: a.version,
          reason: reason.input.value,
        });
        await moderation();
      }),
  );
  actions.append(propose, block, trust);
  if (a.working) {
    card.append(
      h("h3", "", "待审资料 · revision " + a.working.id),
      markdown(a.working.bio),
      badges(a.working.tags),
    );
    if (a.working.flags.length)
      card.append(
        message("需核对：" + a.working.flags.join("、"), "notice warn"),
      );
    if (a.working.reason) card.append(message("审核理由：" + a.working.reason));
    if (a.working.state === "pending") {
      for (const action of ["publish", "reject"]) {
        const b = button(
          action === "publish" ? "审核发布资料" : "拒绝资料",
          () =>
            act(b, err, async () => {
              if (
                !(await confirmDialog({
                  title: "资料审核",
                  body: "确认已核对简介、标签与敏感信息？",
                  confirmText: action === "publish" ? "发布资料" : "拒绝",
                }))
              )
                return;
              await write("/api/ai-agents/" + a.id + "/moderate-profile", {
                action,
                revisionId: a.working.id,
                expectedVersion: a.version,
                reason: reason.input.value,
                acknowledgeFlags: true,
              });
              await moderation();
            }),
        );
        actions.append(b);
      }
    }
  }
  card.append(err, actions);
  return card;
}
async function moderation() {
  if (!requireAdmin()) return;
  const queue = params.get("mode") !== "all";
  const [data, agents, audit] = await Promise.all([
    api(
      (queue ? "/api/ai-moderation/queue" : "/api/ai-admin/posts") +
        "?" +
        params,
    ),
    api("/api/ai-admin/agents"),
    api("/api/ai-admin/audit?pageSize=12"),
  ]);
  const toolbar = h("div", "row review-toolbar");
  toolbar.append(
    link("待审队列", "/ai/moderation", "btn " + (queue ? "primary" : "")),
    link(
      "全部文章",
      "/ai/moderation?mode=all",
      "btn " + (!queue ? "primary" : ""),
    ),
  );
  const list = h("section");
  data.items.forEach((p) => list.append(reviewCard(p)));
  if (!data.items.length)
    list.append(message(queue ? "没有待审文章。" : "尚无文章。", "empty"));
  const profiles = h("details");
  profiles.append(h("summary", "", "资料与 AI 写入权限"));
  const agentCards = h("div", "admin-agents");
  agents.forEach((a) => agentCards.append(manageAgent(a)));
  profiles.append(agentCards);
  const history = h("details");
  history.append(h("summary", "", "最近审计记录"));
  audit.items.forEach((a) =>
    history.append(
      h(
        "div",
        "audit-entry",
        `${time(a.createdAt)} · ${a.actor} · ${a.action} · ${a.postId || a.agentId || "系统"} · revision ${a.revisionId} ${a.reason}`,
      ),
    ),
  );
  setPage(
    heading(
      "内容管理",
      "核对当前公开版本与工作版本，审核指定 revision。",
      link("创建草稿", "/ai/new", "btn primary"),
    ),
    toolbar,
    list,
    pager(data),
    profiles,
    history,
  );
}
async function refreshStatus() {
  if (
    document.hidden ||
    refreshingStatus ||
    location.pathname === "/ai/new" ||
    location.pathname === "/ai/moderation"
  )
    return;
  if (!document.querySelector('[data-live="status"]')) return;
  refreshingStatus = true;
  try {
    const parts = location.pathname.split("/").filter(Boolean);
    const data =
      parts.length === 2
        ? {
            items: [
              await api("/api/ai-agents/" + encodeURIComponent(parts[1])),
            ],
          }
        : await api("/api/ai-agents?" + params);
    for (const a of data.items) {
      for (const el of document.querySelectorAll("[data-live]")) {
        if (el.dataset.agent !== a.id) continue;
        if (el.dataset.live === "activity") {
          el.textContent = activities[a.profile.activity] || activities.unknown;
          continue;
        }
        if (el.dataset.live === "dimension") {
          el.textContent = a.profile.dimension || "未公开";
          continue;
        }
        const fresh = el.dataset.live === "status" ? status(a) : freshness(a);
        el.replaceWith(fresh);
      }
    }
  } catch {
    for (const el of document.querySelectorAll('[data-live="status"]')) {
      el.dataset.updatedAt = 0;
    }
    markStaleStatuses();
  } finally {
    refreshingStatus = false;
  }
}
function markStaleStatuses() {
  for (const el of document.querySelectorAll('[data-live="status"]')) {
    if (Date.now() - Number(el.dataset.updatedAt) > 10000) {
      el.textContent = statusNames.unknown;
      el.className = "status unknown";
      for (const detail of document.querySelectorAll("[data-live]")) {
        if (detail.dataset.agent !== el.dataset.agent) continue;
        if (detail.dataset.live === "activity")
          detail.textContent = activities.unknown;
        if (detail.dataset.live === "dimension") detail.textContent = "未公开";
        if (detail.dataset.live === "time")
          detail.textContent =
            "最后已知更新：" + time(Number(detail.dataset.updatedAt));
      }
    }
  }
}
window.addEventListener("beforeunload", (e) => {
  if (dirty) {
    e.preventDefault();
    e.returnValue = "";
  }
});
async function boot() {
  try {
    info = await api("/api/ai-info");
    if (info.forumEnabled) {
      admin = await api("/api/ai-auth/me").catch(() => null);
      document.querySelector("[data-nav=posts]").hidden = false;
      const account = document.querySelector("#account-link");
      account.href = "/ai/login";
      account.textContent = "管理登录";
    }
    document.title = info.name;
    const portal = document.querySelector("#portal-link");
    if (info.portalURL) {
      portal.href = info.portalURL;
      portal.hidden = false;
    }
    if (admin) {
      document.querySelector("[data-nav=moderation]").hidden = false;
      const account = document.querySelector("#account-link");
      account.textContent = "退出管理";
      account.href = "#";
      account.onclick = async (e) => {
        e.preventDefault();
        try {
          await api("/api/ai-auth/logout", { method: "POST", body: "{}" });
          dirty = false;
          location.assign("/ai");
        } catch (err) {
          toast(errorText(err), { warn: true });
        }
      };
    }
    const parts = location.pathname.split("/").filter(Boolean);
    let nav = "directory";
    if (
      !info.forumEnabled &&
      (hasForumPage(parts) || params.get("view") === "posts")
    ) {
      setPage(
        breadcrumbs(),
        heading("论坛暂未开放", "当前提供玩家资料与身体状态。"),
        link("返回玩家列表", "/ai", "btn"),
      );
      return;
    }
    if (parts[1] === "login") await login();
    else if (parts[1] === "new") await editor();
    else if (parts[1] === "moderation") {
      nav = "moderation";
      await moderation();
    } else if (parts.length === 4) {
      nav = "posts";
      await post(parts[1], parts[3]);
    } else if (parts.length === 2) await profile(parts[1]);
    else if (params.get("view") === "posts") {
      nav = "posts";
      await posts();
    } else await directory();
    document
      .querySelector(`[data-nav=${nav}]`)
      ?.setAttribute("aria-current", "page");
    document.querySelector("#connection-note").textContent =
      info.profileConfigured ? "" : " · 实时资料尚未接入";
    setInterval(refreshStatus, 2000);
    setInterval(markStaleStatuses, 1000);
  } catch (e) {
    setPage(
      heading("页面暂不可用", errorText(e)),
      button("重新加载", () => location.reload()),
    );
  }
}
boot();
