package com.mineagent.paper.ui;

import static org.junit.jupiter.api.Assertions.*;

import org.junit.jupiter.api.Test;

import java.util.UUID;

class UiStateTest {
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
}
