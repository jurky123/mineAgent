package com.mineagent.paper.command;

import java.util.ArrayList;
import java.util.List;
import java.util.Locale;

import org.bukkit.command.Command;
import org.bukkit.command.CommandExecutor;
import org.bukkit.command.CommandSender;
import org.bukkit.command.TabCompleter;

import com.google.gson.JsonObject;
import com.mineagent.paper.ApprovalHandler;
import com.mineagent.paper.BackendClient;
import com.mineagent.paper.Protocol;

import net.kyori.adventure.text.Component;

public final class MineAgentCommand implements CommandExecutor, TabCompleter {

    private final BackendClient backend;
    private final ApprovalHandler approvalHandler;

    public MineAgentCommand(BackendClient backend, ApprovalHandler approvalHandler) {
        this.backend = backend;
        this.approvalHandler = approvalHandler;
    }

    @Override
    public boolean onCommand(CommandSender sender, Command command, String label, String[] args) {
        if (!sender.hasPermission("mineagent.admin")) {
            sender.sendMessage(Component.text("没有权限"));
            return true;
        }
        String sub = args.length > 0 ? args[0].toLowerCase(Locale.ROOT) : "status";
        switch (sub) {
            case "status" -> sender.sendMessage(Component.text(
                    "MineAgent: connected=" + backend.isConnected()
                            + " url=" + backend.url()
                            + " lastError=" + backend.lastError()));
            case "reconnect" -> {
                backend.reconnectNow();
                sender.sendMessage(Component.text("MineAgent: 正在重连..."));
            }
            case "approve" -> resolve(sender, args, true);
            case "deny" -> resolve(sender, args, false);
            default -> sender.sendMessage(Component.text("用法: /mineagent <status|reconnect|approve|deny> [审批ID]"));
        }
        return true;
    }

    private void resolve(CommandSender sender, String[] args, boolean approved) {
        if (!sender.hasPermission("mineagent.approve")) {
            sender.sendMessage(Component.text("没有审批权限"));
            return;
        }
        if (args.length < 2) {
            sender.sendMessage(Component.text("用法: /mineagent " + (approved ? "approve" : "deny") + " <审批ID>"));
            return;
        }
        JsonObject payload = new JsonObject();
        payload.addProperty("approvalId", args[1]);
        payload.addProperty("approved", approved);
        payload.addProperty("operator", sender.getName());
        if (!backend.send(Protocol.APPROVAL_RESULT, payload)) {
            sender.sendMessage(Component.text("MineAgent: 后端未连接，审批失败"));
            return;
        }
        approvalHandler.resolve(args[1]);
        sender.sendMessage(Component.text("MineAgent: 已" + (approved ? "批准" : "拒绝") + " " + args[1]));
    }

    @Override
    public List<String> onTabComplete(CommandSender sender, Command command, String label, String[] args) {
        if (args.length == 1) {
            List<String> subs = new ArrayList<>();
            subs.add("status");
            subs.add("reconnect");
            if (sender.hasPermission("mineagent.approve")) {
                subs.add("approve");
                subs.add("deny");
            }
            return filter(subs, args[0]);
        }
        if (args.length == 2
                && ("approve".equalsIgnoreCase(args[0]) || "deny".equalsIgnoreCase(args[0]))
                && sender.hasPermission("mineagent.approve")) {
            return filter(approvalHandler.pendingIds(), args[1]);
        }
        return List.of();
    }

    private List<String> filter(List<String> options, String prefix) {
        String p = prefix.toLowerCase(Locale.ROOT);
        List<String> out = new ArrayList<>();
        for (String option : options) {
            if (option.toLowerCase(Locale.ROOT).startsWith(p)) {
                out.add(option);
            }
        }
        return out;
    }
}
