package com.mineagent.paper.command;

import java.util.List;

import org.bukkit.command.Command;
import org.bukkit.command.CommandExecutor;
import org.bukkit.command.CommandSender;
import org.bukkit.command.TabCompleter;

import com.mineagent.paper.BackendClient;

import net.kyori.adventure.text.Component;

public final class MineAgentCommand implements CommandExecutor, TabCompleter {

    private final BackendClient backend;

    public MineAgentCommand(BackendClient backend) {
        this.backend = backend;
    }

    @Override
    public boolean onCommand(CommandSender sender, Command command, String label, String[] args) {
        if (!sender.hasPermission("mineagent.admin")) {
            sender.sendMessage(Component.text("没有权限"));
            return true;
        }
        String sub = args.length > 0 ? args[0].toLowerCase() : "status";
        switch (sub) {
            case "status" -> sender.sendMessage(Component.text(
                    "MineAgent: connected=" + backend.isConnected()
                            + " url=" + backend.url()
                            + " lastError=" + backend.lastError()));
            case "reconnect" -> {
                backend.reconnectNow();
                sender.sendMessage(Component.text("MineAgent: 正在重连..."));
            }
            default -> sender.sendMessage(Component.text("用法: /mineagent <status|reconnect>"));
        }
        return true;
    }

    @Override
    public List<String> onTabComplete(CommandSender sender, Command command, String label, String[] args) {
        if (args.length == 1) {
            return List.of("status", "reconnect");
        }
        return List.of();
    }
}
