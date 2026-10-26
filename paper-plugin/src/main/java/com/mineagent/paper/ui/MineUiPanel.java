package com.mineagent.paper.ui;

import com.google.gson.JsonArray;
import com.google.gson.JsonObject;
import com.mineui.api.MineUi;
import com.mineui.api.MineUiProvider;
import com.mineui.api.MineUiSession;
import com.mineui.protocol.SemVer;

import org.bukkit.entity.Player;
import org.bukkit.plugin.Plugin;

import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.function.BiConsumer;

/**
 * Optional adapter. Only this class links MineUI, so vanilla works without the plugin installed.
 */
final class MineUiPanel {
    private final Plugin owner;
    private final MineUi api;
    private final Map<UUID, MineUiSession> sessions = new HashMap<>();

    MineUiPanel(Plugin owner) {
        this.owner = owner;
        this.api = MineUiProvider.get();
    }

    boolean open(
            Player player,
            AgentUi.Panel panel,
            BiConsumer<String, String> action,
            Runnable onClose) {
        if (api == null
                || !api.supportsServerUi(player)
                || SemVer.isOlder(api.modVersion(player), "0.16.1")) return false;
        boolean chatUI = api.supportsChatUi(player);
        owner.getLogger().info("MineUI open: player=" + player.getName()
                + " mod=" + api.modVersion(player)
                + " chat_ui_v1=" + chatUI
                + " caps=" + api.capabilities(player));
        if (!chatUI) return false;
        MineUiSession session = api.open(owner, player, "mineagent", "panel", definition(panel));
        sessions.put(player.getUniqueId(), session);
        session.onClose(
                () -> {
                    sessions.remove(player.getUniqueId(), session);
                    onClose.run();
                });
        for (AgentUi.Choice choice : panel.choices())
            session.on(choice.id(), a -> action.accept(a.id(), ""));
        for (String id : List.of("chat", "body", "approvals", "help"))
            session.on(id, a -> action.accept(a.id(), ""));
        session.on("close", a -> action.accept("close", ""));
        if (panel.input()) session.on("send", a -> action.accept("send", a.string("text", "")));
        update(player, panel);
        session.snapshot();
        return true;
    }

    boolean isOpen(UUID player) {
        MineUiSession s = sessions.get(player);
        return s != null && !s.closed();
    }

    void update(Player player, AgentUi.Panel panel) {
        MineUiSession session = sessions.get(player.getUniqueId());
        if (session == null || session.closed()) return;
        session.batch(
                () -> {
                    session.state("title", panel.title());
                    session.state("status", panel.status());
                    session.state("lines", UiState.lines(panel.content()));
                    session.state("messages", messageItems(panel));
                });
    }

    void close(UUID player) {
        MineUiSession session = sessions.remove(player);
        if (session != null && !session.closed()) session.close();
    }

    static JsonObject definition(AgentUi.Panel panel) {
        return chatDefinition(panel);
    }

    /** 0.17+ layout: flex rows/columns, wrapped message text and a bottom composer. */
    private static JsonObject chatDefinition(AgentUi.Panel panel) {
        JsonObject root = node("row", "100%", "100%");
        root.addProperty("padding", 8);
        root.addProperty("gap", 8);
        root.addProperty("background", "#0B0D12");
        JsonArray rootChildren = new JsonArray();
        root.add("children", rootChildren);

        JsonObject side = node("column", "24%", "100%");
        side.addProperty("padding", 10);
        side.addProperty("gap", 6);
        side.addProperty("radius", 10);
        side.addProperty("background", "#151A22");
        side.add("border", border("#252C38"));
        JsonArray sideChildren = new JsonArray();
        side.add("children", sideChildren);
        sideChildren.add(text("MineAgent", "100%", 18, "#F2F6FC", 1.3));
        sideChildren.add(text("{state.status}", "100%", 11, "#6FCF97", 0.85));
        sideChildren.add(divider());
        sideChildren.add(navButton("chat", "全服对话", true));
        sideChildren.add(navButton("body", "AI 玩家", false));
        sideChildren.add(navButton("approvals", "动作审批", false));
        sideChildren.add(navButton("help", "帮助", false));
        sideChildren.add(spacer());
        sideChildren.add(text("全服公开 · chat_ui_v1", "100%", 10, "#5A6474", 0.75));
        sideChildren.add(navButton("close", "关闭界面", false));
        rootChildren.add(side);

        JsonObject main = node("column", "auto", "100%");
        grow(main, 1);
        main.addProperty("padding", 10);
        main.addProperty("gap", 8);
        main.addProperty("radius", 10);
        main.addProperty("background", "#11151C");
        main.add("border", border("#242B36"));
        JsonArray mainChildren = new JsonArray();
        main.add("children", mainChildren);

        JsonObject header = node("row", "100%", 30);
        header.addProperty("align", "center");
        header.addProperty("gap", 8);
        JsonArray headerChildren = new JsonArray();
        header.add("children", headerChildren);
        JsonObject headings = node("column", "auto", "auto");
        grow(headings, 1);
        headings.addProperty("gap", 2);
        JsonArray headingRows = new JsonArray();
        headings.add("children", headingRows);
        JsonObject title = text("{state.title}", "100%", 15, "#F2F6FC", 1.2);
        title.addProperty("ellipsis", true);
        headingRows.add(title);
        JsonObject status = text("{state.status}", "100%", 11, "#8794A8", 0.85);
        status.addProperty("ellipsis", true);
        headingRows.add(status);
        headerChildren.add(headings);
        headerChildren.add(text("全服可见", "auto", 10, "#6FCF97", 0.8));
        mainChildren.add(header);

        JsonObject list = node("list", "100%", "auto");
        grow(list, 1);
        list.addProperty("items", "{state.messages}");
        list.addProperty("stickToBottom", true);
        list.addProperty("gap", 6);
        list.add("itemTemplate", richMessageTemplate());
        mainChildren.add(list);

        if (panel.input()) {
            JsonObject composer = node("row", "100%", 24);
            composer.addProperty("gap", 6);
            composer.addProperty("align", "center");
            JsonArray composerChildren = new JsonArray();
            composer.add("children", composerChildren);
            JsonObject input = node("input", "auto", 22);
            grow(input, 1);
            input.addProperty("id", "chat_input");
            input.addProperty("radius", 6);
            input.addProperty("background", "#0C1015");
            input.add("border", border("#313C4C"));
            input.addProperty("placeholder", panel.title().contains("身体") ? "输入身体任务…" : "发送消息到全服聊天…");
            input.addProperty("maxLength", 512);
            input.addProperty("action", "send");
            input.addProperty("color", "#E6EDF7");
            composerChildren.add(input);
            JsonObject send = smallButton("send", "发送", 48);
            send.addProperty("height", 22);
            send.addProperty("submit", true);
            send.addProperty("background", "#2E5C8A");
            send.addProperty("hoverBackground", "#3F79B0");
            send.addProperty("color", "#EAF3FB");
            composerChildren.add(send);
            mainChildren.add(composer);
        }

        JsonArray choices = new JsonArray();
        for (AgentUi.Choice choice : panel.choices())
            if (!choice.id().equals("body")) choices.add(choiceButton(choice.id(), choice.label()));
        for (int i = 0; i < choices.size(); i += 3) {
            JsonObject row = node("row", "100%", 18);
            row.addProperty("gap", 4);
            JsonArray cells = new JsonArray();
            for (int j = i; j < Math.min(choices.size(), i + 3); j++) {
                JsonObject cell = choices.get(j).getAsJsonObject();
                cell.addProperty("width", "32%");
                cells.add(cell);
            }
            row.add("children", cells);
            mainChildren.add(row);
        }
        rootChildren.add(main);
        return root;
    }

    private static JsonObject richMessageTemplate() {
        JsonObject template = node("column", "100%", "auto");
        template.addProperty("align", "end");
        JsonArray variants = new JsonArray();
        template.add("children", variants);

        JsonObject user = node("column", "88%", "auto");
        user.addProperty("visible", "{item.isUser}");
        user.addProperty("padding", 8);
        user.addProperty("gap", 4);
        user.addProperty("radius", 8);
        user.addProperty("background", "#1D2B3E");
        user.add("border", border("#31527A"));
        user.add("children", messageBody("{item.author}", "#8CCBFF", "#EAF2FB"));

        JsonObject assistant = node("column", "100%", "auto");
        assistant.addProperty("visible", "{item.isAssistant}");
        assistant.addProperty("padding", 8);
        assistant.addProperty("gap", 4);
        assistant.addProperty("radius", 8);
        assistant.addProperty("background", "#161C24");
        assistant.add("border", border("#2A3441"));
        assistant.add("children", messageBody("{item.author}", "#7EE2B8", "#E4ECF5"));

        JsonObject system = node("column", "100%", "auto");
        system.addProperty("visible", "{item.isSystem}");
        system.addProperty("padding", 6);
        system.addProperty("gap", 2);
        system.addProperty("radius", 6);
        system.addProperty("background", "#0E1218");
        system.addProperty("align", "center");
        system.add("children", messageBody("{item.author}", "#68717F", "#8C97A8"));

        variants.add(user);
        variants.add(assistant);
        variants.add(system);
        return template;
    }

    private static JsonArray messageBody(String author, String authorColor, String textColor) {
        JsonArray rows = new JsonArray();
        JsonObject authorText = text(author, "100%", 10, authorColor, 0.85);
        rows.add(authorText);
        JsonObject body = node("text", "100%", "auto");
        body.addProperty("text", "{item.text}");
        body.addProperty("color", textColor);
        body.addProperty("wrap", true);
        body.addProperty("maxLines", 20);
        body.addProperty("lineHeight", 10);
        rows.add(body);
        return rows;
    }

    private static List<JsonObject> messageItems(AgentUi.Panel panel) {
        List<JsonObject> out = new ArrayList<>();
        if (panel.turns().isEmpty()) {
            out.add(messageItem("系统", "system", panel.content()));
            return out;
        }
        for (UiState.Message turn : panel.turns()) {
            boolean own = turn.author().equals(panel.self());
            out.add(messageItem(own ? "你" : turn.author(), own ? "user" : "assistant", turn.text()));
        }
        return out;
    }

    private static JsonObject messageItem(String author, String role, String text) {
        JsonObject item = new JsonObject();
        item.addProperty("author", author);
        item.addProperty("text", text);
        item.addProperty("isUser", "user".equals(role));
        item.addProperty("isAssistant", "assistant".equals(role));
        item.addProperty("isSystem", "system".equals(role));
        return item;
    }

    private static JsonObject navButton(String id, String label, boolean selected) {
        JsonObject button = node("button", "100%", 18);
        button.addProperty("text", label);
        button.addProperty("action", id);
        button.addProperty("radius", 6);
        button.addProperty("background", selected ? "#243247" : "#1B212B");
        button.addProperty("hoverBackground", selected ? "#2E4059" : "#252E3B");
        button.addProperty("color", selected ? "#BFD8F5" : "#AEB9C9");
        return button;
    }

    private static JsonObject choiceButton(String id, String label) {
        JsonObject button = smallButton(id, label, 36);
        button.addProperty("height", 18);
        button.addProperty("background", "#1B212B");
        button.addProperty("hoverBackground", "#252E3B");
        button.addProperty("color", "#AEB9C9");
        return button;
    }

    private static JsonObject smallButton(String id, String label, int width) {
        JsonObject button = node("button", width, 18);
        button.addProperty("text", label);
        button.addProperty("action", id);
        button.addProperty("radius", 5);
        return button;
    }

    private static JsonObject divider() {
        JsonObject box = node("box", "100%", 1);
        box.addProperty("background", "#252C38");
        return box;
    }

    private static JsonObject spacer() {
        JsonObject spacer = node("spacer", 0, 0);
        grow(spacer, 1);
        return spacer;
    }

    private static JsonObject border(String color) {
        JsonObject border = new JsonObject();
        border.addProperty("color", color);
        border.addProperty("width", 1);
        return border;
    }

    private static void grow(JsonObject node, float grow) {
        node.addProperty("grow", grow);
    }

    private static JsonObject text(String content, Object width, Object height, String color, double scale) {
        JsonObject text = node("text", width, height);
        text.addProperty("text", content);
        text.addProperty("color", color);
        text.addProperty("scale", scale);
        return text;
    }

    private static JsonObject node(String type, Object width, Object height) {
        JsonObject node = new JsonObject();
        node.addProperty("type", type);
        if (width instanceof Number n) node.addProperty("width", n);
        else node.addProperty("width", width.toString());
        if (height instanceof Number n) node.addProperty("height", n);
        else node.addProperty("height", height.toString());
        return node;
    }
}
