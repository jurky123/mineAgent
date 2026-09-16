package com.mineagent.paper;

import com.google.gson.JsonObject;

import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;
import java.util.logging.Logger;

public final class SmokeMain {

    public static void main(String[] args) throws Exception {
        String url = args.length > 0 ? args[0] : "ws://127.0.0.1:8765/ws";
        String token = args.length > 1 ? args[1] : "";
        int waitSeconds = args.length > 2 ? Integer.parseInt(args[2]) : 3;
        String message = args.length > 3 ? args[3] : "hello from smoke test";
        Logger log = Logger.getLogger("smoke");

        BackendClient client = new BackendClient(log, url, token, 300, 2000, 3000, new JdkScheduler());
        client.setHelloInfo("smoke", "smoke", "0.0-test");
        client.setHandler((type, data) -> System.out.println("recv " + type + " " + data));
        client.start();

        for (int i = 0; i < waitSeconds; i++) {
            Thread.sleep(1000);
            System.out.println("t+" + (i + 1) + "s connected=" + client.isConnected() + " error=" + client.lastError());
        }

        JsonObject data = new JsonObject();
        data.addProperty("player", "SmokeBot");
        data.addProperty("uuid", "00000000-0000-0000-0000-000000000001");
        data.addProperty("message", message);
        System.out.println("sent=" + client.send("chat.message", data));

        Thread.sleep(500);
        client.shutdown();
        System.out.println("done");
        System.exit(0);
    }

    static final class JdkScheduler implements BackendClient.TaskScheduler {

        private final ScheduledExecutorService exec = Executors.newSingleThreadScheduledExecutor(r -> {
            Thread t = new Thread(r, "smoke-scheduler");
            t.setDaemon(true);
            return t;
        });

        @Override
        public void schedule(Runnable task, long delayMillis) {
            exec.schedule(task, delayMillis, TimeUnit.MILLISECONDS);
        }

        @Override
        public void shutdown() {
            exec.shutdownNow();
        }
    }
}
