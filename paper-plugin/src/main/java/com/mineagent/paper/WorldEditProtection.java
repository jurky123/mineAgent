package com.mineagent.paper;

import com.sk89q.worldedit.EditSession;
import com.sk89q.worldedit.WorldEdit;
import com.sk89q.worldedit.event.extent.EditSessionEvent;
import com.sk89q.worldedit.util.eventbus.EventHandler.Priority;
import com.sk89q.worldedit.util.eventbus.Subscribe;

import org.bukkit.World;
import org.bukkit.event.EventHandler;
import org.bukkit.event.Listener;
import org.bukkit.event.world.SpawnChangeEvent;
import org.bukkit.event.world.WorldLoadEvent;
import org.bukkit.plugin.java.JavaPlugin;

import java.nio.charset.StandardCharsets;
import java.util.UUID;

public final class WorldEditProtection implements Listener, AutoCloseable {
    private final JavaPlugin plugin;
    private final String player, world;
    private final UUID offlineId;
    private final int radius;
    private volatile SpawnProtectedExtent.Area area;

    public WorldEditProtection(JavaPlugin plugin) {
        this.plugin = plugin;
        player = plugin.getConfig().getString("worldedit-protection.player", "MineAgentBot");
        world = plugin.getConfig().getString("worldedit-protection.world", "world");
        radius = plugin.getConfig().getInt("worldedit-protection.radius", 512);
        if (!player.matches("[A-Za-z0-9_]{3,16}") || radius < 1 || radius > 30000000)
            throw new IllegalArgumentException("Invalid AI WorldEdit protection configuration");
        offlineId =
                UUID.nameUUIDFromBytes(
                        ("OfflinePlayer:" + player).getBytes(StandardCharsets.UTF_8));
        refresh();
        WorldEdit.getInstance().getEventBus().register(this);
        plugin.getServer().getPluginManager().registerEvents(this, plugin);
    }

    private void refresh() {
        World target = plugin.getServer().getWorld(world);
        area =
                target == null
                        ? null
                        : new SpawnProtectedExtent.Area(
                                world,
                                target.getSpawnLocation().getBlockX(),
                                target.getSpawnLocation().getBlockZ(),
                                radius);
        plugin.getLogger()
                .info(
                        "AI WorldEdit protection: player="
                                + player
                                + " world="
                                + world
                                + " radius="
                                + radius
                                + " allHeights=true ready="
                                + (area != null));
    }

    @EventHandler
    public void spawnChanged(SpawnChangeEvent event) {
        if (event.getWorld().getName().equals(world)) refresh();
    }

    @EventHandler
    public void worldLoaded(WorldLoadEvent event) {
        if (event.getWorld().getName().equals(world)) refresh();
    }

    @Subscribe(priority = Priority.VERY_LATE)
    public void editSession(EditSessionEvent event) {
        var actor = event.getActor();
        if (actor == null
                || (!offlineId.equals(actor.getUniqueId()) && !player.equals(actor.getName())))
            return;
        // Both stages: reject before history bookkeeping, and also protect
        // rawSetBlock/smartSetBlock/undo paths which bypass the outer extent.
        if (event.getStage() != EditSession.Stage.BEFORE_HISTORY
                && event.getStage() != EditSession.Stage.BEFORE_CHANGE) return;
        String target = event.getWorld() == null ? "" : event.getWorld().getName();
        event.setExtent(
                new SpawnProtectedExtent(event.getExtent(), target, () -> area, actor::printError));
    }

    @Override
    public void close() {
        WorldEdit.getInstance().getEventBus().unregister(this);
    }
}
