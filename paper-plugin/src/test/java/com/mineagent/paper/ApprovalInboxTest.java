package com.mineagent.paper;

import static org.junit.jupiter.api.Assertions.*;

import org.junit.jupiter.api.Test;

import java.util.concurrent.atomic.AtomicLong;

class ApprovalInboxTest {
    @Test
    void expiresAtBackendDeadlineAndCannotSurviveReconnect() {
        AtomicLong time = new AtomicLong(1000), generation = new AtomicLong(1);
        ApprovalInbox inbox = new ApprovalInbox(time::get, generation::get);
        inbox.add("ap-1", "minecraft_teleport", "Player", "transfer", 2000);
        assertNotNull(inbox.get("ap-1"));
        time.set(3000);
        assertNull(inbox.get("ap-1"));
        inbox.add("ap-2", "minecraft_give", "Player", "give", 2000);
        generation.incrementAndGet();
        assertTrue(inbox.list().isEmpty());
    }

    @Test
    void handledRequestsAndForgedIdsCannotBecomeActiveAgain() {
        ApprovalInbox inbox = new ApprovalInbox(() -> 1000, () -> 1);
        inbox.add("ap-1", "tool", "Player", "prompt", 2000);
        inbox.remove("ap-1");
        assertNull(inbox.get("ap-1"));
        inbox.add("ap-1\n/op Other", "tool", "Player", "prompt", 2000);
        assertTrue(inbox.list().isEmpty());
        for (int i = 0; i < 100; i++) inbox.add("ap-" + i, "tool", "Player", "prompt", 2000);
        assertEquals(64, inbox.list().size());
    }
}
