package com.mineagent.paper;

import java.util.List;

import org.bukkit.command.CommandSender;
import org.bukkit.entity.Player;

import com.google.gson.JsonObject;

import net.kyori.adventure.text.Component;
import net.kyori.adventure.text.event.ClickEvent;
import net.kyori.adventure.text.event.HoverEvent;
import net.kyori.adventure.text.format.NamedTextColor;

public final class ApprovalHandler {

    private final MineAgentPlugin plugin;
    private final ApprovalInbox inbox;

    public ApprovalHandler(MineAgentPlugin plugin) {
        this.plugin = plugin;
        this.inbox = new ApprovalInbox(System::currentTimeMillis, () -> plugin.backend().generation());
    }

    public void request(JsonObject data) {
        String approvalId = str(data, "approvalId");
        String tool = str(data, "tool");
        String requester = str(data, "requester");
        String prompt = str(data, "prompt");
        long timeout = data.has("timeoutMs") ? data.get("timeoutMs").getAsLong() : 180_000L;
        inbox.add(approvalId, tool, requester, prompt, timeout);
        if (inbox.get(approvalId) == null) return;
        plugin.getLogger().info("approval request " + approvalId + ": " + requester + " -> " + prompt);
        plugin.getServer().getScheduler().runTask(plugin, () -> {
            if (inbox.get(approvalId) == null) return;
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
        inbox.remove(approvalId);
    }

    public List<String> pendingIds() {
        return pending().stream().map(ApprovalInbox.Pending::id).toList();
    }

    public List<ApprovalInbox.Pending> pending() { return inbox.list(); }
    public ApprovalInbox.Pending get(String id) { return inbox.get(id); }

    /** Both UI and command use this permission/expiry gate; body approvals never enter this inbox. */
    public boolean decide(CommandSender sender, String id, boolean approved) {
        if (!sender.hasPermission("mineagent.approve")) {
            sender.sendMessage(Component.text("没有审批权限"));
            return false;
        }
        if (inbox.get(id) == null) {
            sender.sendMessage(Component.text("审批已过期、已处理或连接已变化，请刷新列表"));
            return false;
        }
        JsonObject payload = new JsonObject();
        payload.addProperty("approvalId", id);
        payload.addProperty("approved", approved);
        payload.addProperty("operator", sender.getName());
        if (!plugin.backend().send(Protocol.APPROVAL_RESULT, payload)) {
            sender.sendMessage(Component.text("MineAgent: 后端未连接，审批未提交"));
            return false;
        }
        inbox.remove(id);
        sender.sendMessage(Component.text("MineAgent: 已提交" + (approved ? "批准" : "拒绝") + "，实际结果以助手回复为准"));
        return true;
    }

    private String str(JsonObject data, String key) {
        return data.has(key) && !data.get(key).isJsonNull() ? data.get(key).getAsString() : "";
    }
}
