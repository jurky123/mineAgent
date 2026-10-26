package com.mineagent.paper;

import com.google.gson.JsonObject;
import com.sk89q.worldedit.EditSession;
import com.sk89q.worldedit.IncompleteRegionException;
import com.sk89q.worldedit.LocalSession;
import com.sk89q.worldedit.WorldEdit;
import com.sk89q.worldedit.regions.Region;
import com.sk89q.worldedit.event.extent.EditSessionEvent;
import com.sk89q.worldedit.util.eventbus.EventHandler.Priority;
import com.sk89q.worldedit.util.eventbus.Subscribe;

import org.bukkit.World;
import org.bukkit.block.Block;
import org.bukkit.entity.Player;
import org.bukkit.event.EventHandler;
import org.bukkit.event.EventPriority;
import org.bukkit.event.Listener;
import org.bukkit.event.block.BlockBreakEvent;
import org.bukkit.event.block.BlockMultiPlaceEvent;
import org.bukkit.event.block.BlockPlaceEvent;
import org.bukkit.event.world.SpawnChangeEvent;
import org.bukkit.event.world.WorldLoadEvent;
import org.bukkit.plugin.java.JavaPlugin;

import java.nio.charset.StandardCharsets;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.ConcurrentHashMap;

public final class WorldEditProtection implements Listener, AutoCloseable {
    private final JavaPlugin plugin;
    private final String player, world;
    private final UUID offlineId;
    private final int radius;
    private volatile SpawnProtectedExtent.Area area;
    private final Map<String, Long> allowed = new ConcurrentHashMap<>();
    private volatile long areaAllowUntil;

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

    private boolean applies(Player target) {
        return offlineId.equals(target.getUniqueId()) || player.equals(target.getName());
    }

    public JsonObject snapshot(Player target) {
        var current = area;
        var out = new JsonObject();
        out.addProperty("enabled", true);
        out.addProperty("ready", applies(target) && current != null);
        if (current != null) {
            out.addProperty("world", current.world());
            out.addProperty("centerX", current.x());
            out.addProperty("centerZ", current.z());
            out.addProperty("radius", current.radius());
        }
        return out;
    }

    /** One approved protected-area block write; consumed by the next break/place event. */
    public void allowBlock(int x, int z, long ttlMs) {
        allowed.put(x + ":" + z, System.currentTimeMillis() + Math.max(1L, ttlMs));
    }

    /** A short window for one approved WorldEdit command (including its region). */
    public void allowArea(long ttlMs) {
        areaAllowUntil = System.currentTimeMillis() + Math.max(1L, Math.min(15000L, ttlMs));
    }

    private boolean areaAllowed() {
        return System.currentTimeMillis() < areaAllowUntil;
    }

    /** Whether the body's current WorldEdit selection overlaps the spawn guard. */
    public boolean selectionProtected(Player target) {
        if (!applies(target)) return false;
        SpawnProtectedExtent.Area current = area;
        if (current == null) return true;
        try {
            LocalSession session = WorldEdit.getInstance().getSessionManager().findByName(target.getName());
            if (session == null || !session.isSelectionDefined(session.getSelectionWorld())) return false;
            Region region = session.getSelection();
            var min = region.getMinimumPoint();
            var max = region.getMaximumPoint();
            // The body operates in its current Bukkit world; compare the
            // selection bbox against that world's live spawn guard. A selection
            // in another world is conservatively treated as protected.
            return current.intersects(
                    target.getWorld().getName(), min.x(), min.z(), max.x(), max.z());
        } catch (IncompleteRegionException | RuntimeException ignored) {
            return false;
        }
    }

    private boolean consumeAllowed(Block block) {
        String key = block.getX() + ":" + block.getZ();
        Long expires = allowed.get(key);
        if (expires == null) return false;
        if (System.currentTimeMillis() > expires) {
            allowed.remove(key);
            return false;
        }
        allowed.remove(key);
        return true;
    }

    public boolean protects(Player target) {
        return applies(target);
    }

    private boolean blocked(Player target, Block block) {
        if (!applies(target)) return false;
        if (areaAllowed() || consumeAllowed(block)) return false;
        var current = area;
        return current == null
                || current.contains(block.getWorld().getName(), block.getX(), block.getZ());
    }

    @EventHandler(priority = EventPriority.HIGHEST, ignoreCancelled = true)
    public void breakBlock(BlockBreakEvent event) {
        if (blocked(event.getPlayer(), event.getBlock())) {
            event.setCancelled(true);
            event.getPlayer().sendMessage("AI：出生点保护区内禁止破坏方块；可以正常移动。");
        }
    }

    @EventHandler(priority = EventPriority.HIGHEST, ignoreCancelled = true)
    public void placeBlock(BlockPlaceEvent event) {
        boolean deny = blocked(event.getPlayer(), event.getBlockPlaced());
        if (event instanceof BlockMultiPlaceEvent multi) {
            deny |=
                    multi.getReplacedBlockStates().stream()
                            .anyMatch(state -> blocked(event.getPlayer(), state.getBlock()));
        }
        if (deny) {
            event.setCancelled(true);
            event.getPlayer().sendMessage("AI：出生点保护区内禁止放置方块；可以正常移动。");
        }
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
        if (areaAllowed()) return;
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
