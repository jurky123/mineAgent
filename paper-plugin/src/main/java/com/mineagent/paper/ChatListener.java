package com.mineagent.paper;

import com.google.gson.JsonObject;

import io.papermc.paper.event.player.AsyncChatEvent;

import net.kyori.adventure.text.serializer.plain.PlainTextComponentSerializer;

import org.bukkit.entity.Player;
import org.bukkit.event.EventHandler;
import org.bukkit.event.EventPriority;
import org.bukkit.event.Listener;

public final class ChatListener implements Listener {

    private final MineAgentPlugin plugin;

    public ChatListener(MineAgentPlugin plugin) {
        this.plugin = plugin;
    }

    @EventHandler(priority = EventPriority.MONITOR, ignoreCancelled = true)
    public void onChat(AsyncChatEvent event) {
        BackendClient backend = plugin.backend();
        if (backend == null || !backend.isConnected()) {
            return;
        }
        Player player = event.getPlayer();
        String message = PlainTextComponentSerializer.plainText().serialize(event.message());
        JsonObject data = new JsonObject();
        data.addProperty("player", player.getName());
        data.addProperty("uuid", player.getUniqueId().toString());
        data.addProperty("message", message);
        plugin.getServer()
                .getScheduler()
                .runTask(
                        plugin,
                        () -> {
                            if (!player.isOnline()
                                    || plugin.getServer().getPlayer(player.getUniqueId()) != player
                                    || !backend.isConnected()) return;
                            // Public in-game chat. Body tasks use the AI player
                            // panel and keep their own private body-ui session.
                            backend.send(Protocol.CHAT_MESSAGE, data);
                        });
    }
}
