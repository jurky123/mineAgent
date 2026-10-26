package com.mineagent.paper.ui;

import com.google.gson.JsonObject;
import com.mineagent.paper.MineAgentPlugin;

import org.bukkit.entity.Player;

import java.util.HashMap;
import java.util.Map;
import java.util.UUID;

/** Main-thread adapter; no web cookies, passwords or controller bearer tokens enter Minecraft. */
final class BodyUiClient {
    private static final String REQUEST = "body.ui.request";
    private final MineAgentPlugin plugin;
    private final Map<UUID, Connection> connections = new HashMap<>();
    private final Map<String, Pending> pending = new HashMap<>();
    private long generation;

    private static final class Connection {
        final String nonce = UUID.randomUUID().toString();
        long seq, responseSeq, receivedAt;
        boolean allowed;
        JsonObject data = new JsonObject();
        String error = "";
    }

    private record Pending(UUID player, String nonce, long seq, long until, Runnable done) {}

    BodyUiClient(MineAgentPlugin plugin) {
        this.plugin = plugin;
        generation = plugin.backend().generation();
    }

    private void checkGeneration() {
        if (generation != plugin.backend().generation()) {
            generation = plugin.backend().generation();
            connections.clear();
            var previous = java.util.List.copyOf(pending.values());
            pending.clear();
            for (Pending p : previous) p.done().run();
        }
    }

    JsonObject data(Player player) {
        checkGeneration();
        Connection c = connections.get(player.getUniqueId());
        if (c == null) return new JsonObject();
        JsonObject data = c.data.deepCopy();
        boolean expired =
                !plugin.backend().isConnected()
                        || System.currentTimeMillis() - c.receivedAt > 10000
                        || (data.has("expiresAt")
                                && data.get("expiresAt").getAsLong() <= System.currentTimeMillis())
                        || !player.hasPermission("mineagent.body")
                        || !player.hasPermission("mineagent.use");
        if (expired) {
            data.addProperty("linked", false);
            data.remove("snapshot");
            data.remove("approvals");
            data.remove("messages");
        }
        if (!player.hasPermission("mineagent.body")) data.remove("pairCode");
        return data;
    }

    String error(Player player) {
        Connection c = connections.get(player.getUniqueId());
        return c == null ? "" : c.error;
    }

    void poll(Player player, Runnable done) {
        Connection c = connections.get(player.getUniqueId());
        if (c != null && System.currentTimeMillis() - c.receivedAt < 2000) return;
        if (pending.values().stream().anyMatch(p -> p.player().equals(player.getUniqueId())))
            return;
        request(player, "status", null, null, done);
    }

    String chatSession(Player player) {
        checkGeneration();
        Connection c = connections.get(player.getUniqueId());
        return c != null
                        && player.hasPermission("mineagent.use")
                        && player.hasPermission("mineagent.body")
                ? c.nonce
                : "";
    }

    void request(Player player, String action, String text, String approval, Runnable done) {
        checkGeneration();
        if (!player.isOnline() || !player.hasPermission("mineagent.use")) return;
        Connection c = connections.computeIfAbsent(player.getUniqueId(), id -> new Connection());
        if (!"status".equals(action)
                && !"unlink".equals(action)
                && !player.hasPermission("mineagent.body")) {
            c.error = "permission_denied";
            done.run();
            return;
        }
        if (!plugin.backend().isConnected()) {
            c.error = "backend_offline";
            done.run();
            return;
        }
        if (pending.values().stream().filter(p -> p.player().equals(player.getUniqueId())).count()
                        >= 4
                && !"stop".equals(action)) {
            c.error = "busy";
            done.run();
            return;
        }
        JsonObject r = envelope(player, c, action);
        String id = r.get("requestId").getAsString();
        if (text != null) r.addProperty("text", text);
        if (approval != null) r.addProperty("approvalId", approval);
        pending.put(
                id,
                new Pending(
                        player.getUniqueId(),
                        c.nonce,
                        c.seq,
                        System.currentTimeMillis() + 6000,
                        done));
        if (!plugin.backend().send(REQUEST, r)) {
            pending.remove(id);
            c.error = "backend_offline";
            done.run();
        }
    }

    private JsonObject envelope(Player player, Connection c, String action) {
        JsonObject r = new JsonObject();
        r.addProperty("requestId", UUID.randomUUID().toString());
        r.addProperty("uuid", player.getUniqueId().toString());
        r.addProperty("session", c.nonce);
        r.addProperty("player", player.getName());
        c.allowed = player.hasPermission("mineagent.body") && player.hasPermission("mineagent.use");
        r.addProperty("allowed", c.allowed);
        r.addProperty("seq", ++c.seq);
        r.addProperty("action", action);
        return r;
    }

    void receive(JsonObject r) {
        checkGeneration();
        try {
            String id = r.get("requestId").getAsString();
            Pending p = pending.get(id);
            if (p == null) return;
            Connection c = connections.get(p.player());
            Player player = plugin.getServer().getPlayer(p.player());
            if (c == null
                    || player == null
                    || !player.isOnline()
                    || !p.nonce().equals(c.nonce)
                    || !p.nonce().equals(r.get("session").getAsString())
                    || !p.player().toString().equals(r.get("uuid").getAsString())) return;
            if (p.seq() >= c.responseSeq) {
                c.responseSeq = p.seq();
                c.receivedAt = System.currentTimeMillis();
                c.data =
                        r.has("data") && r.get("data").isJsonObject()
                                ? r.getAsJsonObject("data").deepCopy()
                                : new JsonObject();
                c.error = r.has("error") ? r.get("error").getAsString() : "";
            }
            pending.remove(id);
            p.done().run();
        } catch (RuntimeException ignored) {
            /* Malformed responses cannot grant a controller. */
        }
    }

    void tick() {
        checkGeneration();
        for (Map.Entry<String, Pending> e : java.util.List.copyOf(pending.entrySet())) {
            if (e.getValue().until() > System.currentTimeMillis()) continue;
            pending.remove(e.getKey());
            Connection c = connections.get(e.getValue().player());
            if (c != null) c.error = "unconfirmed";
            e.getValue().done().run();
        }
        for (UUID id : java.util.List.copyOf(connections.keySet())) {
            Player player = plugin.getServer().getPlayer(id);
            Connection c = connections.get(id);
            if (player != null
                    && c.allowed
                    && (!player.hasPermission("mineagent.body")
                            || !player.hasPermission("mineagent.use"))) {
                plugin.backend().send(REQUEST, envelope(player, c, "unlink"));
                c.data = new JsonObject();
            }
        }
    }

    void leave(Player player) {
        Connection c = connections.remove(player.getUniqueId());
        if (c != null && generation == plugin.backend().generation())
            plugin.backend().send(REQUEST, envelope(player, c, "leave"));
        pending.entrySet().removeIf(e -> e.getValue().player().equals(player.getUniqueId()));
    }

    void shutdown() {
        for (UUID id : java.util.List.copyOf(connections.keySet())) {
            Player p = plugin.getServer().getPlayer(id);
            if (p != null) leave(p);
        }
        connections.clear();
        pending.clear();
    }
}
