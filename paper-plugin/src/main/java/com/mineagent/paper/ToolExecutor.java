package com.mineagent.paper;

import org.bukkit.Bukkit;
import org.bukkit.Location;
import org.bukkit.World;
import org.bukkit.entity.Player;

import com.google.gson.JsonArray;
import com.google.gson.JsonObject;

public final class ToolExecutor {

    private final MineAgentPlugin plugin;

    public ToolExecutor(MineAgentPlugin plugin) {
        this.plugin = plugin;
    }

    public void handle(JsonObject data) {
        String callId = data.has("callId") ? data.get("callId").getAsString() : "";
        String tool = data.has("tool") ? data.get("tool").getAsString() : "";
        JsonObject args = data.has("args") && data.get("args").isJsonObject()
                ? data.getAsJsonObject("args")
                : new JsonObject();
        plugin.getServer().getScheduler().runTask(plugin, () -> {
            try {
                reply(callId, true, execute(tool, args), null);
            } catch (Exception e) {
                String message = e.getMessage() == null ? e.toString() : e.getMessage();
                reply(callId, false, null, message);
            }
        });
    }

    private JsonObject execute(String tool, JsonObject args) {
        switch (tool) {
            case "minecraft_list_players":
                return listPlayers();
            case "minecraft_player_info":
                return playerInfo(required(args, "player"));
            case "minecraft_server_status":
                return serverStatus();
            case "minecraft_world_time":
                return worldTime();
            case "minecraft_weather":
                return weather();
            default:
                throw new IllegalArgumentException("未知工具: " + tool);
        }
    }

    private JsonObject listPlayers() {
        JsonArray players = new JsonArray();
        for (Player player : Bukkit.getOnlinePlayers()) {
            JsonObject o = new JsonObject();
            o.addProperty("name", player.getName());
            o.addProperty("world", player.getWorld().getName());
            o.addProperty("ping", player.getPing());
            players.add(o);
        }
        JsonObject out = new JsonObject();
        out.addProperty("count", players.size());
        out.add("players", players);
        return out;
    }

    private JsonObject playerInfo(String name) {
        Player player = Bukkit.getPlayerExact(name);
        if (player == null) {
            throw new IllegalArgumentException("玩家 " + name + " 不在线");
        }
        Location loc = player.getLocation();
        JsonObject out = new JsonObject();
        out.addProperty("name", player.getName());
        out.addProperty("world", player.getWorld().getName());
        out.addProperty("x", Math.round(loc.getX()));
        out.addProperty("y", Math.round(loc.getY()));
        out.addProperty("z", Math.round(loc.getZ()));
        out.addProperty("health", player.getHealth());
        out.addProperty("food", player.getFoodLevel());
        out.addProperty("gameMode", player.getGameMode().name());
        out.addProperty("ping", player.getPing());
        out.addProperty("op", player.isOp());
        return out;
    }

    private JsonObject serverStatus() {
        Runtime rt = Runtime.getRuntime();
        long usedMb = (rt.totalMemory() - rt.freeMemory()) / 1024 / 1024;
        long maxMb = rt.maxMemory() / 1024 / 1024;
        double[] tps = Bukkit.getTPS();
        JsonObject out = new JsonObject();
        out.addProperty("tps1m", Math.round(tps[0] * 100.0) / 100.0);
        out.addProperty("tps5m", Math.round(tps[1] * 100.0) / 100.0);
        out.addProperty("tps15m", Math.round(tps[2] * 100.0) / 100.0);
        out.addProperty("online", Bukkit.getOnlinePlayers().size());
        out.addProperty("maxPlayers", Bukkit.getMaxPlayers());
        out.addProperty("usedMemoryMB", usedMb);
        out.addProperty("maxMemoryMB", maxMb);
        out.addProperty("version", Bukkit.getMinecraftVersion());
        return out;
    }

    private JsonObject worldTime() {
        JsonArray worlds = new JsonArray();
        for (World world : Bukkit.getWorlds()) {
            long time = world.getTime();
            JsonObject o = new JsonObject();
            o.addProperty("world", world.getName());
            o.addProperty("time", time);
            o.addProperty("day", world.getFullTime() / 24000L);
            o.addProperty("period", time < 13000 ? "白天" : "夜晚");
            worlds.add(o);
        }
        JsonObject out = new JsonObject();
        out.add("worlds", worlds);
        return out;
    }

    private JsonObject weather() {
        JsonArray worlds = new JsonArray();
        for (World world : Bukkit.getWorlds()) {
            JsonObject o = new JsonObject();
            o.addProperty("world", world.getName());
            o.addProperty("raining", world.hasStorm());
            o.addProperty("thundering", world.isThundering());
            worlds.add(o);
        }
        JsonObject out = new JsonObject();
        out.add("worlds", worlds);
        return out;
    }

    private String required(JsonObject args, String key) {
        if (!args.has(key) || args.get(key).isJsonNull() || args.get(key).getAsString().isBlank()) {
            throw new IllegalArgumentException("缺少参数: " + key);
        }
        return args.get(key).getAsString().trim();
    }

    private void reply(String callId, boolean ok, JsonObject data, String error) {
        JsonObject payload = new JsonObject();
        payload.addProperty("callId", callId);
        payload.addProperty("ok", ok);
        if (data != null) {
            payload.add("data", data);
        }
        if (error != null) {
            payload.addProperty("error", error);
        }
        BackendClient backend = plugin.backend();
        if (backend != null) {
            backend.send(Protocol.TOOL_RESULT, payload);
        }
    }
}
