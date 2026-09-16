package com.mineagent.paper;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;

import org.bukkit.entity.Player;

import com.google.gson.JsonObject;

import net.kyori.adventure.text.Component;
import net.kyori.adventure.text.event.ClickEvent;
import net.kyori.adventure.text.event.HoverEvent;
import net.kyori.adventure.text.format.NamedTextColor;

public final class ApprovalHandler {

    private static final long PENDING_TTL_MS = 5 * 60 * 1000L;

    private final MineAgentPlugin plugin;
    private final Map<String, Long> pending = new ConcurrentHashMap<>();

    public ApprovalHandler(MineAgentPlugin plugin) {
        this.plugin = plugin;
    }

    public void request(JsonObject data) {
        String approvalId = str(data, "approvalId");
        String tool = str(data, "tool");
        String requester = str(data, "requester");
        String prompt = str(data, "prompt");
        if (!approvalId.isEmpty()) {
            pending.put(approvalId, System.currentTimeMillis());
        }
        plugin.getLogger().info("approval request " + approvalId + ": " + requester + " -> " + prompt);
        plugin.getServer().getScheduler().runTask(plugin, () -> {
            Component approve = Component.text("[批准]", NamedTextColor.GREEN)
                    .clickEvent(ClickEvent.runCommand("/mineagent approve " + approvalId))
                    .hoverEvent(HoverEvent.showText(Component.text("批准这次操作，立即执行")));
            Component deny = Component.text("[拒绝]", NamedTextColor.RED)
                    .clickEvent(ClickEvent.runCommand("/mineagent deny " + approvalId))
                    .hoverEvent(HoverEvent.showText(Component.text("拒绝这次操作")));
            Component message = Component.text("[MineAgent] ", NamedTextColor.AQUA)
                    .append(Component.text(requester + " 请求：", NamedTextColor.YELLOW))
                    .append(Component.text(prompt, NamedTextColor.WHITE))
                    .append(Component.text("  "))
                    .append(approve)
                    .append(Component.text(" "))
                    .append(deny)
                    .append(Component.text(" (" + tool + ")", NamedTextColor.DARK_GRAY));
            boolean anyAdmin = false;
            for (Player player : plugin.getServer().getOnlinePlayers()) {
                if (player.hasPermission("mineagent.approve")) {
                    player.sendMessage(message);
                    anyAdmin = true;
                }
            }
            if (!anyAdmin) {
                plugin.getServer().getConsoleSender().sendMessage(message);
            }
        });
    }

    public void resolve(String approvalId) {
        pending.remove(approvalId);
    }

    public List<String> pendingIds() {
        long now = System.currentTimeMillis();
        pending.entrySet().removeIf(entry -> now - entry.getValue() > PENDING_TTL_MS);
        return new ArrayList<>(pending.keySet());
    }

    private String str(JsonObject data, String key) {
        return data.has(key) && !data.get(key).isJsonNull() ? data.get(key).getAsString() : "";
    }
}
