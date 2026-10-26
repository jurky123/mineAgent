package com.mineagent.paper.ui;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.google.gson.JsonObject;

import java.util.List;

import org.junit.jupiter.api.Test;

final class MineUiPanelTest {
    @Test
    void conversationLayoutIsTwoPaneAndBindsChatItems() {
        AgentUi.Panel panel = new AgentUi.Panel(
                "MineAgent",
                "在线",
                "",
                List.of(new AgentUi.Choice("refresh", "刷新")),
                true,
                List.of(new UiState.Message("你", "第一条"), new UiState.Message("MineAgent", "第二条")),
                "你");
        JsonObject root = MineUiPanel.definition(panel);
        assertEquals("row", root.get("type").getAsString());
        assertEquals(2, root.getAsJsonArray("children").size());
        String json = root.toString();
        assertTrue(json.contains("{state.chatItems}"));
        assertTrue(json.contains("全服对话"));
        assertTrue(json.contains("item.h11"));
        assertTrue(json.contains("vanilla:input"));
        assertTrue(json.contains("关闭"));
    }

    @Test
    void chatUiLayoutUsesFlexWrapAndSubmit() {
        AgentUi.Panel panel = new AgentUi.Panel(
                "MineAgent",
                "在线",
                "",
                List.of(new AgentUi.Choice("refresh", "刷新")),
                true,
                List.of(new UiState.Message("你", "第一条"), new UiState.Message("MineAgent", "第二条")),
                "你");
        JsonObject root = MineUiPanel.definition(panel, true);
        String json = root.toString();
        assertTrue(json.contains("\"grow\":1"));
        assertTrue(json.contains("\"wrap\":true"));
        assertTrue(json.contains("\"stickToBottom\":true"));
        assertTrue(json.contains("\"submit\":true"));
        assertTrue(json.contains("{state.messages}"));
        assertTrue(json.contains("chat_ui_v1"));
        assertTrue(json.getBytes(java.nio.charset.StandardCharsets.UTF_8).length < 24 * 1024);
    }

    @Test
    void contentPageFallsBackToSystemCardAndKeepsButtons() {
        AgentUi.Panel panel = new AgentUi.Panel(
                "AI 玩家",
                "在线",
                "状态\n第二行",
                List.of(new AgentUi.Choice("body_task", "下达任务")),
                false);
        JsonObject root = MineUiPanel.definition(panel);
        String json = root.toString();
        assertTrue(json.contains("{state.chatItems}"));
        assertTrue(json.contains("下达任务"));
        assertTrue(json.contains("AI 玩家"));
    }
}
