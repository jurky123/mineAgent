package com.mineagent.paper;

import org.bukkit.command.PluginCommand;
import org.bukkit.entity.Player;
import org.bukkit.plugin.java.JavaPlugin;

import com.google.gson.JsonObject;
import com.mineagent.paper.command.AgentCommand;
import com.mineagent.paper.command.MineAgentCommand;

import net.kyori.adventure.text.Component;
import net.kyori.adventure.text.format.NamedTextColor;

public final class MineAgentPlugin extends JavaPlugin {

    private BackendClient backend;
    private ToolExecutor toolExecutor;
    private ApprovalHandler approvalHandler;

    @Override
    public void onEnable() {
        saveDefaultConfig();
        String url = getConfig().getString("backend.url", "ws://127.0.0.1:8765/ws");
        String token = getConfig().getString("backend.token", "");
        long initial = getConfig().getLong("backend.reconnect.initial-delay-ms", 1000L);
        long max = getConfig().getLong("backend.reconnect.max-delay-ms", 30000L);
        long heartbeat = getConfig().getLong("backend.heartbeat-ms", 20000L);

        backend = new BackendClient(getLogger(), url, token, initial, max, heartbeat, new BukkitSchedulerAdapter(this));
        backend.setHelloInfo("minecraft", getPluginMeta().getVersion(), getServer().getMinecraftVersion());
        backend.setHandler(this::handleBackendMessage);
        backend.start();

        toolExecutor = new ToolExecutor(this);
        approvalHandler = new ApprovalHandler(this);

        getServer().getPluginManager().registerEvents(new ChatListener(this), this);

        PluginCommand command = getCommand("mineagent");
        if (command != null) {
            MineAgentCommand executor = new MineAgentCommand(backend, approvalHandler);
            command.setExecutor(executor);
            command.setTabCompleter(executor);
        }
        PluginCommand agentCommand = getCommand("agent");
        if (agentCommand != null) {
            AgentCommand executor = new AgentCommand(this);
            agentCommand.setExecutor(executor);
            agentCommand.setTabCompleter(executor);
        }
        getLogger().info("MineAgent enabled, backend=" + url);
    }

    @Override
    public void onDisable() {
        if (backend != null) {
            backend.shutdown();
        }
    }

    public BackendClient backend() {
        return backend;
    }

    private void handleBackendMessage(String type, JsonObject data) {
        if (Protocol.AGENT_MESSAGE.equals(type)) {
            handleAgentMessage(data);
        } else if (Protocol.TOOL_CALL.equals(type)) {
            toolExecutor.handle(data);
        } else if (Protocol.APPROVAL_REQUEST.equals(type)) {
            approvalHandler.request(data);
        } else if (!Protocol.HELLO_ACK.equals(type) && !Protocol.PONG.equals(type)) {
            getLogger().info("recv " + type);
        }
    }

    private void handleAgentMessage(JsonObject data) {
        String text = data.has("text") ? data.get("text").getAsString() : "";
        String target = data.has("target") ? data.get("target").getAsString() : "";
        getLogger().info("agent.message -> " + (target.isEmpty() ? "[广播] " : "[" + target + "] ") + text);
        getServer().getScheduler().runTask(this, () -> {
            Component message = Component.text("[MineAgent] ", NamedTextColor.AQUA)
                    .append(Component.text(text, NamedTextColor.WHITE));
            if (target.isEmpty()) {
                getServer().broadcast(message);
                return;
            }
            Player player = getServer().getPlayerExact(target);
            if (player != null) {
                player.sendMessage(message);
            } else {
                getServer().broadcast(message);
            }
        });
    }
}
