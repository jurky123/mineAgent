package com.mineagent.paper;

import org.bukkit.command.PluginCommand;
import org.bukkit.plugin.java.JavaPlugin;

import com.mineagent.paper.command.MineAgentCommand;

public final class MineAgentPlugin extends JavaPlugin {

    private BackendClient backend;

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
        backend.setHandler((type, data) -> getLogger().info("recv " + type));
        backend.start();

        PluginCommand command = getCommand("mineagent");
        if (command != null) {
            MineAgentCommand executor = new MineAgentCommand(backend);
            command.setExecutor(executor);
            command.setTabCompleter(executor);
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
}
