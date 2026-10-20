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
                });
    }

    void close(UUID player) {
        MineUiSession session = sessions.remove(player);
        if (session != null && !session.closed()) session.close();
    }

    static JsonObject definition(AgentUi.Panel panel) {
        // The whole panel can scroll on a small window or a large GUI scale.
        JsonObject root = node("scroll", "100%", "100%");
        JsonObject column = node("column", "100%", "auto");
        column.addProperty("padding", 12);
        column.addProperty("gap", 6);
        JsonArray columns = new JsonArray();
        columns.add(column);
        root.add("children", columns);
        JsonArray children = new JsonArray();
        column.add("children", children);
        JsonObject title = node("text", "100%", 18);
        title.addProperty("text", "{state.title}");
        title.addProperty("scale", 1.3);
        children.add(title);
        JsonObject status = node("text", "100%", 12);
        status.addProperty("text", "{state.status}");
        status.addProperty("color", "#90C8F0");
        children.add(status);
        JsonObject list = node("list", "100%", "42vh");
        list.addProperty("items", "{state.lines}");
        list.addProperty("itemGap", 2);
        list.addProperty("autoScroll", false);
        JsonObject line = node("text", "100%", 11);
        line.addProperty("text", "{item}");
        line.addProperty("color", "#EEEEEE");
        list.add("itemTemplate", line);
        children.add(list);
        if (panel.input()) {
            JsonObject input = node("input", "100%", 22);
            input.addProperty("skin", "vanilla:input");
            input.addProperty("placeholder", "输入问题，回车发送（全服共享）");
            input.addProperty("action", "send");
            input.addProperty("id", "question");
            input.addProperty("maxLength", 512);
            children.add(input);
        }
        JsonArray buttons = new JsonArray();
        for (AgentUi.Choice choice : panel.choices())
            buttons.add(button(choice.id(), choice.label()));
        buttons.add(button("close", "关闭"));
        // Rows instead of a client-specific grid extension: works with older server_ui clients too.
        for (int i = 0; i < buttons.size(); i += 2) {
            JsonObject row = node("row", "100%", 20);
            row.addProperty("gap", 6);
            JsonArray cells = new JsonArray();
            cells.add(buttons.get(i));
            if (i + 1 < buttons.size()) cells.add(buttons.get(i + 1));
            row.add("children", cells);
            children.add(row);
        }
        return root;
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
