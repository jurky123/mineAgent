package com.mineagent.paper.ui;

import static org.junit.jupiter.api.Assertions.*;

import com.google.gson.JsonParser;

import org.junit.jupiter.api.Test;

class BodyPanelsTest {
    @Test
    void unlinkedPlayersGetSummaryAndPairingInsteadOfBodyControls() {
        var data =
                JsonParser.parseString(
                                "{\"enabled\":true,\"linked\":false,\"profile\":{\"displayName\":\"AI\",\"status\":\"online\"}}")
                        .getAsJsonObject();
        var ordinary = BodyPanels.panel("body", data, "", false, "");
        assertFalse(
                ordinary.choices().stream()
                        .anyMatch(
                                c -> c.id().equals("body_stop") || c.id().equals("body_pair_new")));
        var operator = BodyPanels.panel("body", data, "", true, "");
        assertTrue(operator.choices().stream().anyMatch(c -> c.id().equals("body_pair_new")));
        assertFalse(operator.choices().stream().anyMatch(c -> c.id().equals("body_stop")));
    }

    @Test
    void bodyTasksAndApprovalsRemainSeparateFromSharedChatAndMinecraftApprovals() {
        var data =
                JsonParser.parseString(
                                "{\"enabled\":true,\"linked\":true,\"snapshot\":{},\"approvals\":[{\"approvalId\":\"action-1\",\"prompt\":\"Place"
                                    + " one block\"}]}")
                        .getAsJsonObject();
        assertTrue(BodyPanels.panel("body_task", data, "", true, "").input());
        assertTrue(BodyPanels.panel("body_task", data, "", true, "").content().contains("仅在本人"));
        assertTrue(
                BodyPanels.panel("body_approval", data, "", true, "action-1").choices().stream()
                        .anyMatch(c -> c.id().equals("body_approve")));
        assertFalse(
                BodyPanels.panel("body_approval", data, "", true, "expired-id").choices().stream()
                        .anyMatch(c -> c.id().equals("body_approve")));
        assertFalse(BodyPanels.panel("body_task", data, "", false, "").input());
    }

    @Test
    void disabledBackendAndUnconfirmedStopAreNotReportedAsSuccess() {
        var data = JsonParser.parseString("{\"enabled\":false,\"linked\":false}").getAsJsonObject();
        var disabled = BodyPanels.panel("body", data, "", true, "");
        assertEquals("尚未启用", disabled.status());
        assertFalse(
                disabled.choices().stream()
                        .anyMatch(c -> c.id().equals("body_task") || c.id().equals("body_stop")));
        assertTrue(BodyPanels.error("stop_unconfirmed").contains("尚未确认"));
    }
}
