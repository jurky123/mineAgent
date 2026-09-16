package com.mineagent.paper;

import com.google.gson.Gson;
import com.google.gson.JsonObject;

final class Protocol {

    static final int VERSION = 1;

    static final String HELLO = "hello";
    static final String CHAT_MESSAGE = "chat.message";
    static final String TOOL_RESULT = "tool.result";
    static final String APPROVAL_RESULT = "approval.result";
    static final String PONG = "pong";

    static final String HELLO_ACK = "hello_ack";
    static final String AGENT_MESSAGE = "agent.message";
    static final String TOOL_CALL = "tool.call";
    static final String APPROVAL_REQUEST = "approval.request";
    static final String PING = "ping";

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
