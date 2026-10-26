package com.mineagent.paper.ui;

import com.google.gson.JsonObject;
import com.mineagent.paper.ApprovalInbox;
import com.mineagent.paper.MineAgentPlugin;
import com.mineagent.paper.Protocol;

import io.papermc.paper.dialog.Dialog;
import io.papermc.paper.registry.data.dialog.ActionButton;
import io.papermc.paper.registry.data.dialog.DialogBase;
import io.papermc.paper.registry.data.dialog.action.DialogAction;
import io.papermc.paper.registry.data.dialog.body.DialogBody;
import io.papermc.paper.registry.data.dialog.input.DialogInput;
import io.papermc.paper.registry.data.dialog.type.DialogType;

import net.kyori.adventure.text.Component;
import net.kyori.adventure.text.event.ClickCallback;
import net.kyori.adventure.text.event.ClickEvent;
import net.kyori.adventure.text.format.NamedTextColor;
import net.kyori.adventure.text.format.TextDecoration;

import org.bukkit.entity.Player;
import org.bukkit.event.EventHandler;
import org.bukkit.event.Listener;
import org.bukkit.event.player.PlayerQuitEvent;
import org.bukkit.scheduler.BukkitTask;

import java.net.URI;
import java.time.Duration;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;

/**
 * Two renderers, one permission-checked action path. All Bukkit/UI access stays on the main thread.
 */
public final class AgentUi implements Listener {
    public record Choice(String id, String label) {}

    public record Panel(
            String title,
            String status,
            String content,
            List<Choice> choices,
            boolean input,
            List<UiState.Message> turns,
            String self) {
        public Panel(
                String title, String status, String content, List<Choice> choices, boolean input) {
            this(title, status, content, choices, input, List.of(), "");
        }
    }

    private record View(UUID token, String page, int offset, String approvalId, boolean vanilla) {}

    private final MineAgentPlugin plugin;
    private final UiState state = new UiState();
    private final Map<UUID, View> views = new HashMap<>();
    private final BukkitTask ticker;
    private MineUiPanel enhanced;
    private final BodyUiClient bodyClient;
    private final String portal;

    public String bodyChatSession(Player player) {
        return bodyClient.chatSession(player);
    }

    public AgentUi(MineAgentPlugin plugin) {
        this.plugin = plugin;
        bodyClient = new BodyUiClient(plugin);
        String base = plugin.getConfig().getString("ui.portal-url", "").strip();
        portal = validPortal(base);
        if (!base.isEmpty() && portal.isEmpty())
            plugin.getLogger().warning("ui.portal-url 无效，网页入口已隐藏（仅支持无凭据/参数的 HTTPS 地址）");
        if (!"dialog".equalsIgnoreCase(plugin.getConfig().getString("ui.mode", "auto"))
                && plugin.getServer().getPluginManager().isPluginEnabled("MineUI")) {
            try {
                enhanced = new MineUiPanel(plugin);
            } catch (LinkageError | RuntimeException e) {
                plugin.getLogger().warning("MineUI 不可用，使用原版对话框");
            }
        }
        plugin.getServer().getPluginManager().registerEvents(this, plugin);
        ticker =
                plugin.getServer()
                        .getScheduler()
                        .runTaskTimer(
                                plugin,
                                () -> {
                                    bodyClient.tick();
                                    refreshEnhanced();
                                },
                                20L,
                                20L);
    }

    public static String validPortal(String base) {
        if (base.isEmpty()) return "";
        try {
            URI u = URI.create(base);
            if (!"https".equals(u.getScheme())
                    || u.getHost() == null
                    || u.getRawUserInfo() != null
                    || u.getRawQuery() != null
                    || u.getRawFragment() != null) return "";
            return base.replaceAll("/+$", "");
        } catch (IllegalArgumentException e) {
            return "";
        }
    }

    public void open(Player player, boolean vanilla) {
        show(player, "chat", 0, "", vanilla);
    }

    private void show(Player player, String page, int offset, String approvalId, boolean vanilla) {
        if (!player.hasPermission("mineagent.use")) {
            player.sendMessage(Component.text("没有助手使用权限"));
            return;
        }
        UUID id = player.getUniqueId(), token = state.open(id);
        View view = new View(token, page, Math.max(0, offset), approvalId, vanilla);
        views.put(id, view);
        Panel panel = panel(player, view);
        if (!vanilla && enhanced != null) {
            try {
                if (enhanced.open(
                        player,
                        panel,
                        (action, input) -> handle(player, token, action, input),
                        () -> closed(id, token))) return;
            } catch (LinkageError | RuntimeException e) {
                plugin.getLogger().warning("MineUI 打开失败，使用原版对话框");
            }
        }
        if (enhanced != null) enhanced.close(id);
        // A partially opened MineUI session may have run its close hook during fallback.
        UUID fallbackToken = state.open(id);
        View fallback = new View(fallbackToken, page, Math.max(0, offset), approvalId, true);
        views.put(id, fallback);
        dialog(player, fallback, panel);
    }

    private Panel panel(Player player, View view) {
        if (view.page().equals("body") || view.page().startsWith("body_"))
            return BodyPanels.panel(
                    view.page(),
                    bodyClient.data(player),
                    bodyClient.error(player),
                    player.hasPermission("mineagent.body"),
                    view.approvalId());
        List<Choice> buttons = new ArrayList<>();
        String content;
        boolean input = false;
        String title = "MineAgent · 助手";
        switch (view.page()) {
            case "home", "chat" -> {
                title = "MineAgent · 对话";
                input = true;
                content = history(player, 0);
                buttons.add(new Choice("refresh", "刷新回复"));
                buttons.add(new Choice("history", "本次在线记录"));
                buttons.add(new Choice("body", "AI 玩家"));
                buttons.add(new Choice("menu", "更多功能"));
            }
            case "history" -> {
                title = "本次在线记录";
                content = history(player, view.offset());
                if (state.history(player.getUniqueId()).size()
                        > (view.offset() + 1) * UiState.PAGE_SIZE)
                    buttons.add(new Choice("older", "更早记录"));
                if (view.offset() > 0) buttons.add(new Choice("newer", "更新记录"));
                buttons.add(new Choice("chat", "继续提问"));
                buttons.add(new Choice("refresh", "刷新"));
            }
            case "queries" -> {
                title = "常用查询";
                content = "查询由现有 Agent 工具完成；回复仍遵循服务器的聊天设置。\n传送、给物品等请求请在提问框描述，仍按原权限与审批执行。";
                for (Choice choice :
                        List.of(
                                new Choice("query_status", "服务器状态"),
                                new Choice("query_players", "在线玩家"),
                                new Choice("query_weather", "时间和天气"),
                                new Choice("query_me", "我的状态"),
                                new Choice("query_memory", "共享记忆概况"),
                                new Choice("query_summary", "共享记忆摘要"),
                                new Choice("query_usage", "模型额度"),
                                new Choice("query_identity", "我的身份"))) buttons.add(choice);
            }
            case "approvals", "approval" -> {
                title = "操作审批";
                if (!player.hasPermission("mineagent.approve")) {
                    content = "没有审批权限。";
                    break;
                }
                if ("approval".equals(view.page())) {
                    ApprovalInbox.Pending pending = plugin.approvals().get(view.approvalId());
                    if (pending == null) content = "此请求已过期、已处理或连接已变化。请返回并刷新。";
                    else {
                        long seconds =
                                Math.max(
                                        0,
                                        (pending.expiresAt() - System.currentTimeMillis()) / 1000);
                        content =
                                "请求者："
                                        + UiState.clip(pending.requester(), 100)
                                        + "\n操作："
                                        + UiState.clip(pending.tool(), 100)
                                        + "\n\n"
                                        + UiState.clip(pending.prompt(), 2000)
                                        + "\n\n约 "
                                        + seconds
                                        + " 秒后过期。批准只适用于这一项请求。";
                        buttons.add(new Choice("approve", "批准本次请求"));
                        buttons.add(new Choice("deny", "拒绝本次请求"));
                    }
                    buttons.add(new Choice("approvals", "返回审批列表"));
                } else {
                    List<ApprovalInbox.Pending> pending = plugin.approvals().pending();
                    content = "仅显示现有 Minecraft 操作审批。\nAI 身体动作请从 AI 玩家 → 动作审批查看，需先关联管理员。\n\n";
                    int from = view.offset() * 5;
                    if (pending.isEmpty()) content += "暂无待审批请求。";
                    else if (from >= pending.size()) content += "列表已变化，请刷新。";
                    for (int i = from; i < Math.min(pending.size(), from + 5); i++) {
                        ApprovalInbox.Pending p = pending.get(i);
                        content +=
                                (i + 1)
                                        + ". "
                                        + UiState.clip(p.requester(), 40)
                                        + " · "
                                        + UiState.clip(p.prompt(), 100)
                                        + "\n";
                        buttons.add(new Choice("request:" + p.id(), "查看请求 " + (i + 1)));
                    }
                    if (pending.size() > from + 5) buttons.add(new Choice("older", "下一页"));
                    if (view.offset() > 0) buttons.add(new Choice("newer", "上一页"));
                    buttons.add(new Choice("refresh", "刷新审批"));
                }
            }
            case "help" ->
                    content =
                            "直接输入 /agent 打开面板，/agent dialog 强制原版界面。\n"
                                    + "/agent <问题> 和聊天 @agent 仍可用。\n\n"
                                    + "增强界面在输入框按回车发送；原版对话框点击发送。\n"
                                    + "原版对话框通过刷新查看新回复，按钮刷新会保留未发送草稿。\n"
                                    + "仅保存本次在线、本人实际收到的最近 30 条记录，离线即清除。\n\n"
                                    + "AI 玩家面板首次关联已验证网页管理员后可控制；离线或授权到期后失效，游戏内 OP 不能代替网页认证。";
            default -> {
                title = "MineAgent · 更多功能";
                content = "查询服务器、查看审批与打开网页。\n返回对话即可继续输入，未发送的原版面板草稿会保留。";
                buttons.add(new Choice("chat", "继续对话"));
                buttons.add(new Choice("queries", "常用查询"));
                buttons.add(new Choice("history", "本次在线记录"));
                buttons.add(new Choice("body", "AI 玩家"));
                if (player.hasPermission("mineagent.approve"))
                    buttons.add(
                            new Choice(
                                    "approvals", "待审批 · " + plugin.approvals().pending().size()));
                if (player.hasPermission("mineagent.admin"))
                    buttons.add(new Choice("reconnect", "重连后端"));
                if (!portal.isEmpty()) {
                    buttons.add(new Choice("portal", "网页助手"));
                }
                buttons.add(new Choice("help", "使用说明"));
            }
        }
        boolean conversation = "home".equals(view.page()) || "chat".equals(view.page());
        if (!conversation) buttons.add(new Choice("home", "返回对话"));
        List<UiState.Message> turns =
                conversation || "history".equals(view.page())
                        ? state.historyPage(player.getUniqueId(), conversation ? 0 : view.offset())
                        : List.of();
        return new Panel(
                title,
                plugin.backend().isConnected()
                        ? conversation ? "全服共享 · 已连接 · 发送后可刷新回复" : "后端已连接"
                        : "后端未连接 · 输入会保留，请稍后刷新",
                content,
                List.copyOf(buttons),
                input,
                turns,
                player.getName());
    }

    private String history(Player player, int page) {
        List<UiState.Message> entries = state.historyPage(player.getUniqueId(), page);
        if (entries.isEmpty())
            return "想聊些什么？\n可以询问服务器状态、在线玩家，或描述需要 AI 完成的任务。\n\n这里只显示你本次在线收到的消息；对话内容可能向全服公开。";
        StringBuilder text = new StringBuilder();
        for (UiState.Message m : entries)
            text.append(m.author().equals(player.getName()) ? "你" : m.author())
                    .append("：\n")
                    .append(m.text())
                    .append("\n\n");
        return text.toString();
    }

    private void dialog(Player player, View view, Panel panel) {
        List<ActionButton> buttons = new ArrayList<>();
        if (panel.input())
            buttons.add(
                    button(
                            player,
                            view,
                            "send",
                            "body_task".equals(view.page()) ? "发送任务" : "发送问题"));
        for (Choice choice : panel.choices())
            buttons.add(button(player, view, choice.id(), choice.label()));
        DialogBase base =
                DialogBase.builder(Component.text(panel.title()))
                        .body(dialogBody(panel))
                        .inputs(
                                panel.input()
                                        ? List.of(
                                                DialogInput.text(
                                                                "question",
                                                                Component.text(
                                                                        "body_task"
                                                                                        .equals(
                                                                                                view
                                                                                                        .page())
                                                                                ? "任务（仅本人）"
                                                                                : "问题（全服共享）"))
                                                        .width(310)
                                                        .initial(
                                                                state.draft(
                                                                        player.getUniqueId(),
                                                                        "body_task"
                                                                                .equals(
                                                                                        view
                                                                                                .page())))
                                                        .maxLength(512)
                                                        .build())
                                        : List.of())
                        .canCloseWithEscape(true)
                        .pause(false)
                        .afterAction(DialogBase.DialogAfterAction.CLOSE)
                        .build();
        player.showDialog(
                Dialog.create(
                        b ->
                                b.empty()
                                        .base(base)
                                        .type(
                                                DialogType.multiAction(
                                                        buttons,
                                                        button(player, view, "close", "关闭"),
                                                        2))));
    }

    private List<DialogBody> dialogBody(Panel panel) {
        List<DialogBody> body = new ArrayList<>();
        body.add(DialogBody.plainMessage(Component.text(panel.status(), NamedTextColor.GRAY), 310));
        if (panel.turns().isEmpty())
            body.add(DialogBody.plainMessage(Component.text(panel.content()), 310));
        else
            for (UiState.Message turn : panel.turns()) {
                boolean own = turn.author().equals(panel.self());
                Component message =
                        Component.text(
                                        own ? "你" : turn.author(),
                                        own ? NamedTextColor.GOLD : NamedTextColor.AQUA)
                                .decorate(TextDecoration.BOLD)
                                .append(Component.newline())
                                .append(
                                        Component.text(turn.text(), NamedTextColor.WHITE)
                                                .decoration(TextDecoration.BOLD, false));
                body.add(DialogBody.plainMessage(message, 310));
            }
        return List.copyOf(body);
    }

    private ActionButton button(Player player, View view, String action, String label) {
        UUID owner = player.getUniqueId();
        return ActionButton.builder(Component.text(label))
                .width(150)
                .action(
                        DialogAction.customClick(
                                (response, audience) -> {
                                    if (!(audience instanceof Player sender)
                                            || !owner.equals(sender.getUniqueId())) return;
                                    String input = response.getText("question");
                                    if (plugin.isEnabled())
                                        plugin.getServer()
                                                .getScheduler()
                                                .runTask(
                                                        plugin,
                                                        () ->
                                                                handle(
                                                                        sender,
                                                                        view.token(),
                                                                        action,
                                                                        input));
                                },
                                ClickCallback.Options.builder()
                                        .uses(1)
                                        .lifetime(Duration.ofMinutes(5))
                                        .build()))
                .build();
    }

    private void handle(Player player, UUID token, String action, String input) {
        UUID id = player.getUniqueId();
        View view = views.get(id);
        if (!player.isOnline() || !state.current(id, token) || view == null) return;
        if (!player.hasPermission("mineagent.use")) {
            close(player);
            return;
        }
        Panel current = panel(player, view);
        if (current.input()) state.draft(id, "body_task".equals(view.page()), input);
        if ("close".equals(action)) {
            close(player);
            return;
        }
        if (!("send".equals(action) && current.input())
                && current.choices().stream().noneMatch(c -> c.id().equals(action))) {
            player.sendMessage(Component.text("请求或权限已变化，已刷新界面"));
            show(player, view.page(), view.offset(), view.approvalId(), view.vanilla());
            return;
        }
        switch (action) {
            case "send" -> {
                if ("body_task".equals(view.page())) {
                    try {
                        bodyRequest(
                                player,
                                view,
                                "task",
                                UiState.question(input),
                                null,
                                "body_task",
                                "");
                    } catch (IllegalArgumentException e) {
                        player.sendMessage(Component.text(e.getMessage()));
                        if (enhanced == null || !enhanced.isOpen(id))
                            show(player, "body_task", 0, "", view.vanilla());
                    }
                    break;
                }
                boolean sent = submit(player, input);
                if (sent) state.draft(id, false, "");
                // Do not rebuild a MineUI input on a rejected request; preserve the draft.
                if (sent || enhanced == null || !enhanced.isOpen(id))
                    show(player, "chat", 0, "", view.vanilla());
            }
            case "refresh" -> {
                if (enhanced != null
                        && enhanced.isOpen(id)
                        && (view.page().equals("chat") || view.page().equals("home")))
                    enhanced.update(player, panel(player, view));
                else show(player, view.page(), view.offset(), view.approvalId(), view.vanilla());
            }
            case "older" -> show(player, view.page(), view.offset() + 1, "", view.vanilla());
            case "newer" ->
                    show(player, view.page(), Math.max(0, view.offset() - 1), "", view.vanilla());
            case "approve", "deny" -> {
                plugin.approvals().decide(player, view.approvalId(), "approve".equals(action));
                show(player, "approvals", 0, "", view.vanilla());
            }
            case "reconnect" -> {
                if (player.hasPermission("mineagent.admin")) {
                    plugin.backend().reconnectNow();
                    player.sendMessage(Component.text("正在重连后端…"));
                }
                show(player, "home", 0, "", view.vanilla());
            }
            case "portal" -> link(player, "/agent");
            case "body",
                    "body_task",
                    "body_approvals",
                    "body_confirm_resume",
                    "body_confirm_disconnect" ->
                    bodyRequest(player, view, "status", null, null, action, "");
            case "body_refresh" ->
                    bodyRequest(player, view, "status", null, null, view.page(), view.approvalId());
            case "body_pair_new" -> bodyRequest(player, view, "pair", null, null, "body_pair", "");
            case "body_stop", "body_pause", "body_resume", "body_disconnect", "body_unlink" ->
                    bodyRequest(player, view, action.substring(5), null, null, "body", "");
            case "body_approve", "body_deny" ->
                    bodyRequest(
                            player,
                            view,
                            action.substring(5),
                            null,
                            view.approvalId(),
                            "body_approvals",
                            "");
            case "body_web" -> bodyWeb(player);
            case "query_status",
                    "query_players",
                    "query_weather",
                    "query_me",
                    "query_memory",
                    "query_summary",
                    "query_usage",
                    "query_identity" -> {
                String question =
                        switch (action) {
                            case "query_players" -> "在线有谁？";
                            case "query_weather" -> "现在游戏时间和天气怎么样？";
                            case "query_me" -> "查看玩家 " + player.getName() + " 的位置、生命和饥饿值";
                            case "query_memory" -> "/memory";
                            case "query_summary" -> "/memory summary";
                            case "query_usage" -> "/usage";
                            case "query_identity" -> "/myid";
                            default -> "/status";
                        };
                submit(player, question);
                show(player, "chat", 0, "", view.vanilla());
            }
            default -> {
                if (action.startsWith("body_request:"))
                    bodyRequest(
                            player,
                            view,
                            "status",
                            null,
                            null,
                            "body_approval",
                            action.substring(13));
                else if (action.startsWith("request:"))
                    show(player, "approval", 0, action.substring(8), view.vanilla());
                else show(player, action, 0, "", view.vanilla());
            }
        }
    }

    public void bodyMessage(JsonObject data) {
        bodyClient.receive(data);
    }

    private void bodyRequest(
            Player player,
            View view,
            String action,
            String text,
            String approval,
            String page,
            String detail) {
        player.sendActionBar(Component.text("正在读取 AI 玩家…", NamedTextColor.AQUA));
        bodyClient.request(
                player,
                action,
                text,
                approval,
                () -> {
                    if (player.isOnline() && state.current(player.getUniqueId(), view.token())) {
                        if ("task".equals(action) && bodyClient.error(player).isEmpty())
                            state.draft(player.getUniqueId(), true, "");
                        if ("status".equals(action)
                                && "body_task".equals(page)
                                && page.equals(view.page())
                                && enhanced != null
                                && enhanced.isOpen(player.getUniqueId()))
                            enhanced.update(player, panel(player, view));
                        else show(player, page, 0, detail, view.vanilla());
                    }
                });
    }

    private void bodyWeb(Player player) {
        JsonObject data = bodyClient.data(player);
        String base = validPortal(BodyPanels.text(data, "portalURL"));
        if (base.isEmpty()) base = portal;
        String code = BodyPanels.text(data, "pairCode");
        if (base.isEmpty() || !player.hasPermission("mineagent.body") || code.length() != 32) {
            player.sendMessage(Component.text("请在网站 /body 输入面板中的临时代码。"));
            return;
        }
        openLink(player, base + "/body#game-code=" + code);
    }

    public boolean submit(Player player, String raw) {
        if (!player.hasPermission("mineagent.use")) {
            player.sendMessage(Component.text("没有助手使用权限"));
            return false;
        }
        String question;
        try {
            question = UiState.question(raw);
        } catch (IllegalArgumentException e) {
            player.sendMessage(Component.text(e.getMessage(), NamedTextColor.YELLOW));
            return false;
        }
        if (!plugin.backend().isConnected()) {
            player.sendMessage(Component.text("后端未连接，问题未发送", NamedTextColor.YELLOW));
            return false;
        }
        if (!state.maySend(player.getUniqueId(), System.currentTimeMillis())) {
            player.sendMessage(Component.text("请稍等片刻再发送", NamedTextColor.YELLOW));
            return false;
        }
        JsonObject data = new JsonObject();
        data.addProperty("player", player.getName());
        data.addProperty("uuid", player.getUniqueId().toString());
        data.addProperty("world", player.getWorld().getName());
        data.addProperty("message", "@agent " + question);
        // Public in-game chat: do not attach the body-ui nonce. Body tasks use
        // the AI player panel/body action path and stay out of the public feed.
        if (!plugin.backend().send(Protocol.CHAT_MESSAGE, data)) {
            player.sendMessage(Component.text("发送失败，问题未进入后端队列"));
            return false;
        }
        plugin.getServer()
                .broadcast(
                        Component.text(player.getName() + ": ", NamedTextColor.YELLOW)
                                .append(Component.text(question, NamedTextColor.WHITE)));
        for (Player viewer : plugin.getServer().getOnlinePlayers())
            state.record(viewer.getUniqueId(), player.getName(), question);
        player.sendActionBar(Component.text("问题已提交，等待助手回复…", NamedTextColor.AQUA));
        refreshEnhanced();
        return true;
    }

    public void reply(Player viewer, String text) {
        state.record(viewer.getUniqueId(), "MineAgent", text);
        View view = views.get(viewer.getUniqueId());
        if (view != null)
            viewer.sendActionBar(Component.text("助手已回复 · 面板内刷新查看", NamedTextColor.AQUA));
        if (enhanced != null && view != null) {
            try {
                enhanced.update(viewer, panel(viewer, view));
            } catch (LinkageError | RuntimeException e) {
                enhanced.close(viewer.getUniqueId());
            }
        }
    }

    private void link(Player player, String path) {
        if (portal.isEmpty()) return;
        openLink(player, portal + path);
    }

    private void openLink(Player player, String url) {
        close(player);
        ActionButton open =
                ActionButton.builder(Component.text("在浏览器打开"))
                        .action(DialogAction.staticAction(ClickEvent.openUrl(url)))
                        .build();
        player.showDialog(
                Dialog.create(
                        b ->
                                b.empty()
                                        .base(
                                                DialogBase.builder(Component.text("Mine 门户"))
                                                        .body(
                                                                List.of(
                                                                        DialogBody.plainMessage(
                                                                                Component.text(
                                                                                        "在浏览器完成登录。游戏内权限不会替代网页身份验证。"))))
                                                        .canCloseWithEscape(true)
                                                        .pause(false)
                                                        .build())
                                        .type(DialogType.notice(open))));
    }

    private void refreshEnhanced() {
        if (enhanced == null) return;
        for (Map.Entry<UUID, View> entry : List.copyOf(views.entrySet())) {
            Player player = plugin.getServer().getPlayer(entry.getKey());
            if (player != null && player.isOnline()) {
                if (!player.hasPermission("mineagent.use")) {
                    close(player);
                    continue;
                }
                try {
                    View view = entry.getValue();
                    if (enhanced.isOpen(entry.getKey())
                            && ("body".equals(view.page()) || "body_task".equals(view.page())))
                        bodyClient.poll(
                                player,
                                () -> {
                                    if (state.current(player.getUniqueId(), view.token())
                                            && enhanced.isOpen(player.getUniqueId()))
                                        enhanced.update(player, panel(player, view));
                                });
                    enhanced.update(player, panel(player, entry.getValue()));
                } catch (LinkageError | RuntimeException e) {
                    enhanced.close(player.getUniqueId());
                }
            }
        }
    }

    private void closed(UUID id, UUID token) {
        if (state.current(id, token)) {
            state.close(id, token);
            views.remove(id);
        }
    }

    public void close(Player player) {
        View view = views.remove(player.getUniqueId());
        if (view != null) state.close(player.getUniqueId(), view.token());
        if (enhanced != null) enhanced.close(player.getUniqueId());
        player.closeDialog();
    }

    @EventHandler
    public void onQuit(PlayerQuitEvent e) {
        bodyClient.leave(e.getPlayer());
        if (enhanced != null) enhanced.close(e.getPlayer().getUniqueId());
        views.remove(e.getPlayer().getUniqueId());
        state.leave(e.getPlayer().getUniqueId());
    }

    public void shutdown() {
        bodyClient.shutdown();
        ticker.cancel();
        for (UUID id : List.copyOf(views.keySet())) {
            Player player = plugin.getServer().getPlayer(id);
            if (player != null) close(player);
        }
    }
}
