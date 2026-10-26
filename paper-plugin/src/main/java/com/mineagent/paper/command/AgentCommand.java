package com.mineagent.paper.command;

import java.util.ArrayList;
import java.util.List;
import java.util.Locale;

import org.bukkit.Bukkit;
import org.bukkit.command.Command;
import org.bukkit.command.CommandExecutor;
import org.bukkit.command.CommandSender;
import org.bukkit.command.TabCompleter;
import org.bukkit.entity.Player;

import com.mineagent.paper.MineAgentPlugin;

import net.kyori.adventure.text.Component;

public final class AgentCommand implements CommandExecutor, TabCompleter {

    private static final List<String> SUGGESTIONS = List.of(
            "ui", "dialog", "在线有谁", "服务器状态", "现在天气", "游戏时间", "查玩家");

    private final MineAgentPlugin plugin;

    public AgentCommand(MineAgentPlugin plugin) {
        this.plugin = plugin;
    }

    @Override
    public boolean onCommand(CommandSender sender, Command command, String label, String[] args) {
        if (!(sender instanceof Player player)) {
            sender.sendMessage(Component.text("只有游戏内玩家可以使用 /agent"));
            return true;
        }
        if (args.length == 0 || (args.length == 1 && ("ui".equalsIgnoreCase(args[0]) || "dialog".equalsIgnoreCase(args[0])))) {
            // Default is now the MineUI 0.17 chat panel. Keep dialog only as an
            // explicit emergency fallback: /agent dialog.
            plugin.ui().open(player, args.length == 1 && "dialog".equalsIgnoreCase(args[0]));
        } else plugin.ui().submit(player, String.join(" ", args));
        return true;
    }

    @Override
    public List<String> onTabComplete(CommandSender sender, Command command, String label, String[] args) {
        if (args.length == 1) {
            return filter(SUGGESTIONS, args[0]);
        }
        if (args.length == 2 && "查玩家".equals(args[0])) {
            List<String> names = new ArrayList<>();
            for (Player player : Bukkit.getOnlinePlayers()) {
                names.add(player.getName());
            }
            return filter(names, args[1]);
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
