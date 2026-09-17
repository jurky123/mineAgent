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

import com.google.gson.JsonObject;
import com.mineagent.paper.BackendClient;
import com.mineagent.paper.MineAgentPlugin;
import com.mineagent.paper.Protocol;

import net.kyori.adventure.text.Component;
import net.kyori.adventure.text.format.NamedTextColor;

public final class AgentCommand implements CommandExecutor, TabCompleter {

    private static final List<String> SUGGESTIONS = List.of(
            "在线有谁", "服务器状态", "现在天气", "游戏时间", "查玩家");

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
        BackendClient backend = plugin.backend();
        if (backend == null || !backend.isConnected()) {
            player.sendMessage(Component.text("[MineAgent] 后端未连接，请稍后再试"));
            return true;
        }
        if (args.length == 0) {
            player.sendMessage(Component.text("用法: /agent <问题>，也可以直接聊天发 @agent <问题>"));
            return true;
        }
        String question = String.join(" ", args).trim();
        if (question.isEmpty()) {
            player.sendMessage(Component.text("用法: /agent <问题>"));
            return true;
        }
        JsonObject data = new JsonObject();
        data.addProperty("player", player.getName());
        data.addProperty("uuid", player.getUniqueId().toString());
        data.addProperty("world", player.getWorld().getName());
        data.addProperty("message", "@agent " + question);
        if (!backend.send(Protocol.CHAT_MESSAGE, data)) {
            player.sendMessage(Component.text("[MineAgent] 发送失败，后端未连接"));
            return true;
        }
        plugin.getServer().broadcast(Component.text(player.getName() + ": ", NamedTextColor.YELLOW)
                .append(Component.text(question, NamedTextColor.WHITE)));
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
