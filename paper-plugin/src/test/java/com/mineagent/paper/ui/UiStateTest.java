package com.mineagent.paper.ui;

import static org.junit.jupiter.api.Assertions.*;

import org.junit.jupiter.api.Test;

import java.util.UUID;

class UiStateTest {
    @Test
    void refreshingOrNavigatingPreservesSeparateDraftsButLeavingClearsThem() {
        UiState state = new UiState();
        UUID alice = UUID.randomUUID(), bob = UUID.randomUUID();
        state.draft(alice, false, "共享问题草稿");
        state.draft(alice, true, "私密施工任务");
        state.open(alice);
        state.open(alice);
        assertEquals("共享问题草稿", state.draft(alice, false));
        assertEquals("私密施工任务", state.draft(alice, true));
        assertEquals("", state.draft(bob, true));
        state.draft(alice, true, "");
        assertEquals("", state.draft(alice, true));
        assertEquals("共享问题草稿", state.draft(alice, false));
        state.leave(alice);
        assertEquals("", state.draft(alice, false));
    }

    @Test
    void conversationPagesKeepChronologyWithoutDroppingIntermediateMessages() {
        UiState state = new UiState();
        UUID alice = UUID.randomUUID();
        for (int i = 0; i < 10; i++) state.record(alice, "player", "message " + i);
        assertEquals("message 6", state.historyPage(alice, 0).getFirst().text());
        assertEquals("message 9", state.historyPage(alice, 0).getLast().text());
        assertEquals("message 2", state.historyPage(alice, 1).getFirst().text());
        assertEquals("message 1", state.historyPage(alice, 2).getLast().text());
        assertTrue(state.historyPage(alice, 3).isEmpty());
    }

    @Test
    void callbacksAreBoundToPlayerAndCurrentScreen() {
        UiState state = new UiState();
        UUID alice = UUID.randomUUID(), bob = UUID.randomUUID();
        UUID old = state.open(alice);
        assertFalse(state.current(bob, old));
        UUID current = state.open(alice);
        state.close(alice, old); // Old renderer close hooks must not invalidate a new screen.
        assertFalse(state.current(alice, old));
        assertTrue(state.current(alice, current));
        state.leave(alice);
        assertFalse(state.current(alice, current));
    }

    @Test
    void historiesArePrivateBoundedAndClearedOnQuit() {
        UiState state = new UiState();
        UUID alice = UUID.randomUUID(), bob = UUID.randomUUID();
        state.record(alice, "Agent", "private reply");
        assertTrue(state.history(bob).isEmpty());
        for (int i = 0; i < 40; i++) state.record(alice, "Agent", "message " + i);
        assertEquals(30, state.history(alice).size());
        assertEquals("message 39", state.history(alice).getLast().text());
        state.leave(alice);
        assertTrue(state.history(alice).isEmpty());
    }

    @Test
    void craftedInputAndRapidDuplicateSubmissionsAreRejected() {
        assertThrows(IllegalArgumentException.class, () -> UiState.question("hello\n/op somebody"));
        assertThrows(IllegalArgumentException.class, () -> UiState.question("x".repeat(513)));
        assertThrows(IllegalArgumentException.class, () -> UiState.question("   "));
        assertEquals("你好", UiState.question("  你好  "));
        UiState state = new UiState();
        UUID alice = UUID.randomUUID(), bob = UUID.randomUUID();
        assertTrue(state.maySend(alice, 1000));
        assertFalse(state.maySend(alice, 2000));
        assertTrue(state.maySend(bob, 2000));
        assertTrue(state.maySend(alice, 2500));
    }

    @Test
    void unicodeAndGameTextRemainLiteralBoundedLines() {
        String text = "{state.secret} /mineagent approve forged\n" + "你😀".repeat(5000);
        var lines = UiState.lines(text);
        assertTrue(lines.getFirst().contains("{state.secret}"));
        assertTrue(lines.size() <= 250);
        assertTrue(lines.stream().allMatch(line -> line.codePoints().count() <= 50));
    }

    @Test
    void longPrivateConversationsKeepTheNewestReplyVisible() {
        var lines = UiState.lines("旧消息\n".repeat(400) + "最新回复", true);
        assertEquals(250, lines.size());
        assertEquals("最新回复", lines.getLast());
    }
}
