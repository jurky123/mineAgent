package com.mineagent.paper;

import org.bukkit.plugin.Plugin;

final class BukkitSchedulerAdapter implements BackendClient.TaskScheduler {

    private final Plugin plugin;

    BukkitSchedulerAdapter(Plugin plugin) {
        this.plugin = plugin;
    }

    @Override
    public void schedule(Runnable task, long delayMillis) {
        long ticks = Math.max(0L, delayMillis / 50L);
        plugin.getServer().getScheduler().runTaskLaterAsynchronously(plugin, task, ticks);
    }

    @Override
    public void shutdown() {
    }
}
