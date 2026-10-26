package com.mineagent.paper.ui;

import com.google.gson.JsonArray;
import com.google.gson.JsonObject;
import com.mineui.api.MineUi;
import com.mineui.api.MineUiProvider;
import com.mineui.api.MineUiSession;
import com.mineui.protocol.SemVer;

import org.bukkit.entity.Player;
import org.bukkit.plugin.Plugin;

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
                    session.state("chatLines", chatLines(panel));
                });
    }

    void close(UUID player) {
        MineUiSession session = sessions.remove(player);
        if (session != null && !session.closed()) session.close();
    }

    static JsonObject definition(AgentUi.Panel panel) {
        // Conversation composer stays outside the scrollable transcript.
        JsonObject root = node(panel.input() ? "column" : "scroll", "100%", "100%");
        root.addProperty("background", "#E0161B22");
        JsonObject column = node("column", "100%", panel.input() ? "100%" : "auto");
        column.addProperty("padding", 8);
        column.addProperty("gap", 4);
        if (panel.input()) column.addProperty("justify", "space-between");
        JsonArray columns = new JsonArray();
        columns.add(column);
        root.add("children", columns);
        JsonArray children = new JsonArray();
        column.add("children", children);
        JsonObject header = node("column", "100%", "auto");
        header.addProperty("gap", 4);
        JsonArray heading = new JsonArray();
        header.add("children", heading);
        children.add(header);
        JsonObject title = node("text", "100%", 16);
        title.addProperty("text", "{state.title}");
        title.addProperty("scale", 1.3);
        heading.add(title);
        JsonObject status = node("text", "100%", 12);
        status.addProperty("text", "{state.status}");
        status.addProperty("color", "#C1D6E6");
        heading.add(status);
        JsonObject list = node("list", "100%", panel.input() ? "54%" : "42vh");
        list.addProperty("items", "{state.chatLines}");
        list.addProperty("itemGap", 3);
        list.addProperty("autoScroll", false);
        JsonObject line = node("stack", "100%", 11);
        JsonArray variants = new JsonArray();
        for (String[] style :
                new String[][] {{"body", "#EEEEEE"}, {"own", "#FFD275"}, {"agent", "#83D9DB"}}) {
            JsonObject text = node("text", "100%", 11);
            text.addProperty("text", "{item.text}");
            text.addProperty("color", style[1]);
            text.addProperty("visible", "item." + style[0]);
            variants.add(text);
        }
        line.add("children", variants);
        list.add("itemTemplate", line);
        children.add(list);
        JsonObject footer = node("column", "100%", "auto");
        footer.addProperty("gap", 4);
        JsonArray controls = new JsonArray();
        footer.add("children", controls);
        children.add(footer);
        if (panel.input()) {
            JsonObject input = node("input", "100%", 22);
            input.addProperty("skin", "vanilla:input");
            input.addProperty("placeholder", "输入消息，按回车发送");
            input.addProperty("action", "send");
            input.addProperty("id", "question");
            input.addProperty("maxLength", 512);
            controls.add(input);
        }
        JsonArray buttons = new JsonArray();
        for (AgentUi.Choice choice : panel.choices())
            buttons.add(button(choice.id(), choice.label()));
        buttons.add(button("close", "关闭"));
        // Rows instead of a client-specific grid extension: works with older server_ui clients too.
        int count = panel.input() ? 3 : 2;
        for (int i = 0; i < buttons.size(); i += count) {
            JsonObject row = node("row", "100%", 20);
            row.addProperty("gap", 4);
            JsonArray cells = new JsonArray();
            for (int j = i; j < Math.min(buttons.size(), i + count); j++) {
                JsonObject cell = buttons.get(j).getAsJsonObject();
                cell.addProperty("width", count == 3 ? "31%" : "48%");
                cells.add(cell);
            }
            row.add("children", cells);
            controls.add(row);
        }
        return root;
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
