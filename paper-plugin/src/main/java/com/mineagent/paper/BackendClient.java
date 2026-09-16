package com.mineagent.paper;

import com.google.gson.JsonObject;
import com.google.gson.JsonParser;

import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.WebSocket;
import java.nio.ByteBuffer;
import java.time.Duration;
import java.util.concurrent.CompletionStage;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.logging.Logger;

public final class BackendClient {

    public interface MessageHandler {
        void onMessage(String type, JsonObject data);
    }

    public interface TaskScheduler {
        void schedule(Runnable task, long delayMillis);

        void shutdown();
    }

    private final Logger log;
    private final URI uri;
    private final String token;
    private final long initialDelayMs;
    private final long maxDelayMs;
    private final long heartbeatMs;
    private final TaskScheduler scheduler;
    private final HttpClient http;

    private final Object sendLock = new Object();
    private final AtomicBoolean running = new AtomicBoolean();
    private final AtomicBoolean reconnectScheduled = new AtomicBoolean();
    private final AtomicInteger attempt = new AtomicInteger();
    private final StringBuilder partial = new StringBuilder();

    private volatile WebSocket ws;
    private volatile boolean connected;
    private volatile long lastIncoming;
    private volatile String lastError = "";
    private volatile MessageHandler handler = (type, data) -> {
    };
    private volatile String role = "minecraft";
    private volatile String pluginVersion = "dev";
    private volatile String serverVersion = "";

    public BackendClient(Logger log, String url, String token, long initialDelayMs, long maxDelayMs,
                         long heartbeatMs, TaskScheduler scheduler) {
        this.log = log;
        this.uri = URI.create(url);
        this.token = token == null ? "" : token;
        this.initialDelayMs = initialDelayMs;
        this.maxDelayMs = maxDelayMs;
        this.heartbeatMs = heartbeatMs;
        this.scheduler = scheduler;
        this.http = HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(10)).build();
    }

    public void setHandler(MessageHandler handler) {
        this.handler = handler;
    }

    public void setHelloInfo(String role, String pluginVersion, String serverVersion) {
        this.role = role;
        this.pluginVersion = pluginVersion;
        this.serverVersion = serverVersion;
    }

    public boolean isConnected() {
        return connected;
    }

    public String lastError() {
        return lastError;
    }

    public String url() {
        return uri.toString();
    }

    public void start() {
        if (!running.compareAndSet(false, true)) {
            return;
        }
        connect();
        scheduler.schedule(this::heartbeatTick, heartbeatMs);
    }

    public void shutdown() {
        if (!running.compareAndSet(true, false)) {
            return;
        }
        WebSocket socket = ws;
        if (socket != null) {
            socket.sendClose(WebSocket.NORMAL_CLOSURE, "plugin disable");
        }
        connected = false;
        ws = null;
        scheduler.shutdown();
    }

    public void reconnectNow() {
        WebSocket socket = ws;
        if (socket != null) {
            socket.abort();
        }
        handleDisconnect(socket, "manual reconnect");
    }

    public boolean send(String type, JsonObject data) {
        WebSocket socket = ws;
        if (socket == null || !connected || !running.get()) {
            lastError = "not connected";
            return false;
        }
        String payload = Protocol.envelope(type, data);
        try {
            synchronized (sendLock) {
                socket.sendText(payload, true);
            }
            return true;
        } catch (Exception e) {
            handleDisconnect(socket, "send failed: " + e.getMessage());
            return false;
        }
    }

    private void connect() {
        if (!running.get()) {
            return;
        }
        log.info("connecting to " + uri);
        http.newWebSocketBuilder()
                .connectTimeout(Duration.ofSeconds(10))
                .buildAsync(uri, new Listener())
                .whenComplete((socket, error) -> {
                    if (error != null) {
                        lastError = String.valueOf(error.getMessage());
                        log.warning("connect failed: " + lastError);
                        scheduleReconnect(attempt.incrementAndGet());
                    }
                });
    }

    private void handleDisconnect(WebSocket socket, String reason) {
        if (ws != socket && ws != null) {
            return;
        }
        ws = null;
        connected = false;
        lastError = reason;
        if (!running.get()) {
            return;
        }
        log.info("backend disconnected: " + reason);
        scheduleReconnect(attempt.incrementAndGet());
    }

    private void scheduleReconnect(int n) {
        if (!running.get()) {
            return;
        }
        if (!reconnectScheduled.compareAndSet(false, true)) {
            return;
        }
        long delay = Math.min(maxDelayMs, initialDelayMs * (1L << Math.min(n, 6)));
        log.info("reconnect in " + delay + "ms");
        scheduler.schedule(() -> {
            reconnectScheduled.set(false);
            connect();
        }, delay);
    }

    private void heartbeatTick() {
        if (!running.get()) {
            return;
        }
        WebSocket socket = ws;
        if (socket != null && connected) {
            if (System.currentTimeMillis() - lastIncoming > heartbeatMs * 3) {
                socket.abort();
                handleDisconnect(socket, "heartbeat timeout");
            } else {
                try {
                    synchronized (sendLock) {
                        socket.sendPing(ByteBuffer.allocate(0));
                    }
                } catch (Exception e) {
                    handleDisconnect(socket, "ping failed: " + e.getMessage());
                }
            }
        }
        scheduler.schedule(this::heartbeatTick, heartbeatMs);
    }

    private void sendHello() {
        JsonObject data = new JsonObject();
        data.addProperty("protocol", Protocol.VERSION);
        data.addProperty("role", role);
        data.addProperty("plugin", "MineAgent");
        data.addProperty("pluginVersion", pluginVersion);
        data.addProperty("serverVersion", serverVersion);
        data.addProperty("token", token);
        send(Protocol.HELLO, data);
    }

    private void handleText(String text) {
        JsonObject env;
        try {
            env = JsonParser.parseString(text).getAsJsonObject();
        } catch (Exception e) {
            log.warning("invalid message: " + e.getMessage());
            return;
        }
        String type = env.has("type") ? env.get("type").getAsString() : "";
        JsonObject data = env.has("data") && env.get("data").isJsonObject()
                ? env.getAsJsonObject("data")
                : new JsonObject();
        if (Protocol.PING.equals(type)) {
            send(Protocol.PONG, new JsonObject());
            return;
        }
        if (Protocol.HELLO_ACK.equals(type)) {
            log.info("handshake ok, backend=" + data.get("backendVersion"));
        }
        handler.onMessage(type, data);
    }

    private final class Listener implements WebSocket.Listener {

        @Override
        public void onOpen(WebSocket socket) {
            ws = socket;
            connected = true;
            lastError = "";
            lastIncoming = System.currentTimeMillis();
            attempt.set(0);
            reconnectScheduled.set(false);
            partial.setLength(0);
            log.info("connected to " + uri);
            sendHello();
            socket.request(1);
        }

        @Override
        public CompletionStage<?> onText(WebSocket socket, CharSequence data, boolean last) {
            lastIncoming = System.currentTimeMillis();
            partial.append(data);
            if (!last) {
                socket.request(1);
                return null;
            }
            String text = partial.toString();
            partial.setLength(0);
            handleText(text);
            socket.request(1);
            return null;
        }

        @Override
        public CompletionStage<?> onPong(WebSocket socket, ByteBuffer message) {
            lastIncoming = System.currentTimeMillis();
            socket.request(1);
            return null;
        }

        @Override
        public CompletionStage<?> onClose(WebSocket socket, int statusCode, String reason) {
            handleDisconnect(socket, "closed " + statusCode + (reason.isEmpty() ? "" : " " + reason));
            return null;
        }

        @Override
        public void onError(WebSocket socket, Throwable error) {
            handleDisconnect(socket, "error " + error);
        }
    }
}
