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
        // A vanilla fallback must not receive a clear-dialog packet before its new dialog.
        player.closeDialog();
        close(player.getUniqueId());
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
                    List<JsonObject> items = chatItems(panel);
                    session.state("chatItems", items);
                    session.state("chatHighlight", Math.max(0, items.size() - 1));
                });
    }

    void close(UUID player) {
        MineUiSession session = sessions.remove(player);
        if (session != null && !session.closed()) session.close();
    }

    static JsonObject definition(AgentUi.Panel panel) {
        JsonObject root = node("row", "100%", "100%");
        root.addProperty("background", "#E010141C");
        root.addProperty("padding", 6);
        root.addProperty("gap", 6);
        JsonArray rootChildren = new JsonArray();
        root.add("children", rootChildren);

        // Left rail: same role as DSH's session sidebar, but for the single
        // public Minecraft conversation plus body/approval entry points.
        JsonObject side = node("column", "22%", "100%");
        side.addProperty("background", "#C8121822");
        side.addProperty("padding", 6);
        side.addProperty("gap", 5);
        side.addProperty("clip", true);
        JsonArray sideChildren = new JsonArray();
        side.add("children", sideChildren);
        sideChildren.add(text("{state.title}", "100%", 18, "#FFFFFF", 1.25));
        sideChildren.add(text("{state.status}", "100%", 11, "#9FB3C8", 1));
        sideChildren.add(sideButton("chat", "全服对话"));
        sideChildren.add(sideButton("body", "AI 玩家"));
        sideChildren.add(sideButton("approvals", "动作审批"));
        sideChildren.add(sideButton("help", "帮助"));
        sideChildren.add(sideButton("close", "关闭"));
        rootChildren.add(side);

        // Main column: header, transcript, composer.
        JsonObject main = node("column", "78%", "100%");
        main.addProperty("gap", 5);
        JsonArray mainChildren = new JsonArray();
        main.add("children", mainChildren);
        JsonObject header = node("row", "100%", 22);
        header.addProperty("gap", 4);
        JsonArray headerChildren = new JsonArray();
        header.add("children", headerChildren);
        headerChildren.add(text("{state.title}", "70%", 15, "#FFFFFF", 1.1));
        headerChildren.add(text("{state.status}", "30%", 11, "#9FB3C8", 1));
        mainChildren.add(header);

        JsonObject list = node("list", "100%", panel.input() ? "54%" : "70%");
        list.addProperty("items", "{state.chatItems}");
        list.addProperty("itemGap", 4);
        list.addProperty("autoScroll", true);
        list.addProperty("highlightIndex", "{state.chatHighlight}");
        list.add("itemTemplate", chatCardTemplate());
        mainChildren.add(list);

        JsonObject composer = node("column", "100%", "auto");
        composer.addProperty("gap", 4);
        JsonArray composerChildren = new JsonArray();
        composer.add("children", composerChildren);
        if (panel.input()) {
            JsonObject input = node("input", "100%", 20);
            input.addProperty("skin", "vanilla:input");
            input.addProperty("placeholder", panel.title().contains("身体") ? "输入身体任务，按回车发送" : "输入消息，按回车发送（全服可见）");
            input.addProperty("action", "send");
            input.addProperty("id", "question");
            input.addProperty("maxLength", 512);
            composerChildren.add(input);
        }
        JsonArray buttons = new JsonArray();
        for (AgentUi.Choice choice : panel.choices())
            if (!choice.id().equals("body")) buttons.add(button(choice.id(), choice.label()));
        for (int i = 0; i < buttons.size(); i += 3) {
            JsonObject row = node("row", "100%", 20);
            row.addProperty("gap", 4);
            JsonArray cells = new JsonArray();
            for (int j = i; j < Math.min(buttons.size(), i + 3); j++) {
                JsonObject cell = buttons.get(j).getAsJsonObject();
                cell.addProperty("width", "32%");
                cells.add(cell);
            }
            row.add("children", cells);
            composerChildren.add(row);
        }
        mainChildren.add(composer);
        rootChildren.add(main);
        return root;
    }

    private static JsonObject chatCardTemplate() {
        JsonObject stack = node("stack", "100%", "auto");
        JsonArray cards = new JsonArray();
        String[][] styles = {
            {"own", "#FF2B4C6F", "#FFD275"},
            {"agent", "#FF24343F", "#83D9DB"},
            {"system", "#FF3A2F2F", "#C1D6E6"},
        };
        for (String[] style : styles) {
            JsonObject card = node("column", "100%", "auto");
            card.addProperty("background", style[1]);
            card.addProperty("radius", 6);
            card.addProperty("padding", 5);
            card.addProperty("gap", 2);
            card.addProperty("visible", "item." + style[0]);
            JsonArray rows = new JsonArray();
            card.add("children", rows);
            rows.add(text("{item.author}", "100%", 11, style[2], 1));
            for (int i = 0; i < 12; i++) {
                JsonObject line = text("{item.l" + i + "}", "100%", 10, "#E6EDF3", 1);
                line.addProperty("visible", "item.h" + i);
                rows.add(line);
            }
            cards.add(card);
        }
        stack.add("children", cards);
        return stack;
    }

    private static List<JsonObject> chatItems(AgentUi.Panel panel) {
        List<JsonObject> out = new ArrayList<>();
        if (panel.turns().isEmpty()) {
            out.add(chatItem("系统", "system", UiState.lines(panel.content(), panel.input())));
            return out;
        }
        for (UiState.Message turn : panel.turns()) {
            boolean own = turn.author().equals(panel.self());
            out.add(chatItem(own ? "你" : turn.author(), own ? "own" : "agent", UiState.lines(turn.text())));
        }
        return out;
    }

    private static JsonObject chatItem(String author, String role, List<String> lines) {
        JsonObject item = new JsonObject();
        item.addProperty("author", author);
        for (String kind : new String[] {"own", "agent", "system"})
            item.addProperty(kind, kind.equals(role));
        for (int i = 0; i < 12; i++) {
            String line = i < lines.size() ? lines.get(i) : " ";
            item.addProperty("l" + i, line);
            item.addProperty("h" + i, i < lines.size());
        }
        return item;
    }

    private static JsonObject sideButton(String id, String label) {
        JsonObject button = node("button", "100%", 20);
        button.addProperty("skin", "vanilla:button");
        button.addProperty("text", label);
        button.addProperty("action", id);
        return button;
    }

    private static JsonObject text(String content, Object width, Object height, String color, double scale) {
        JsonObject text = node("text", width, height);
        text.addProperty("text", content);
        text.addProperty("color", color);
        text.addProperty("scale", scale);
        return text;
    }

    static List<JsonObject> chatLines(AgentUi.Panel panel) {
        java.util.ArrayList<JsonObject> lines = new java.util.ArrayList<>();
        if (panel.turns().isEmpty()) {
            for (String text : UiState.lines(panel.content(), panel.input()))
                lines.add(chatLine(text, "body"));
        } else
            for (UiState.Message turn : panel.turns()) {
                boolean own = turn.author().equals(panel.self());
                lines.add(chatLine(own ? "你" : turn.author(), own ? "own" : "agent"));
                for (String text : UiState.lines(turn.text())) lines.add(chatLine(text, "body"));
                lines.add(chatLine(" ", "body"));
            }
        return lines.stream().limit(512).toList();
    }

    private static JsonObject chatLine(String text, String role) {
        JsonObject line = new JsonObject();
        line.addProperty("text", text);
        for (String kind : new String[] {"body", "own", "agent"})
            line.addProperty(kind, kind.equals(role));
        return line;
    }

    private static JsonObject button(String id, String label) {
        JsonObject button = node("button", "48%", 20);
        button.addProperty("skin", "vanilla:button");
        button.addProperty("text", label);
        button.addProperty("action", id);
        return button;
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
