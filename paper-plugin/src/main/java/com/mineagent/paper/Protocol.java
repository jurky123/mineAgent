package com.mineagent.paper;

import com.google.gson.Gson;
import com.google.gson.JsonObject;

public final class Protocol {

    public static final int VERSION = 1;

    public static final String HELLO = "hello";
    public static final String CHAT_MESSAGE = "chat.message";
    public static final String TOOL_RESULT = "tool.result";
    public static final String APPROVAL_RESULT = "approval.result";
    public static final String PONG = "pong";

    public static final String HELLO_ACK = "hello_ack";
    public static final String AGENT_MESSAGE = "agent.message";
    public static final String TOOL_CALL = "tool.call";
    public static final String APPROVAL_REQUEST = "approval.request";
    public static final String PING = "ping";

    private static final Gson GSON = new Gson();

    private Protocol() {
    }

    static String envelope(String type, JsonObject data) {
        JsonObject env = new JsonObject();
        env.addProperty("v", VERSION);
        env.addProperty("type", type);
        env.addProperty("ts", System.currentTimeMillis());
        if (data != null) {
            env.add("data", data);
        }
        return GSON.toJson(env);
    }
}
