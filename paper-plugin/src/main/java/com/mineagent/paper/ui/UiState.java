package com.mineagent.paper.ui;

import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.Deque;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;

/** Main-thread, online-session-only history and callback guards. Never a second agent session. */
public final class UiState {
    public static final int PAGE_SIZE = 4;

    public record Message(String author, String text) {}

    private final Map<UUID, Deque<Message>> history = new HashMap<>();
    private final Map<UUID, UUID> screens = new HashMap<>();
    private final Map<UUID, Long> sentAt = new HashMap<>();
    private final Map<UUID, String> chatDrafts = new HashMap<>(), taskDrafts = new HashMap<>();

    public UUID open(UUID player) {
        UUID token = UUID.randomUUID();
        screens.put(player, token);
        return token;
    }

    public boolean current(UUID player, UUID token) {
        return token != null && token.equals(screens.get(player));
    }

    public void close(UUID player, UUID token) {
        screens.remove(player, token);
    }

    public void leave(UUID player) {
        screens.remove(player);
        history.remove(player);
        sentAt.remove(player);
        chatDrafts.remove(player);
        taskDrafts.remove(player);
    }

    public void record(UUID viewer, String author, String text) {
        Deque<Message> entries = history.computeIfAbsent(viewer, k -> new ArrayDeque<>());
        entries.addLast(new Message(author, clip(text, 1600)));
        while (entries.size() > 30) entries.removeFirst();
    }

    public List<Message> history(UUID player) {
        return List.copyOf(history.getOrDefault(player, new ArrayDeque<>()));
    }

    public List<Message> historyPage(UUID player, int page) {
        List<Message> entries = history(player);
        int end = Math.max(0, entries.size() - Math.max(0, page) * PAGE_SIZE);
        return entries.subList(Math.max(0, end - PAGE_SIZE), end);
    }

    public String draft(UUID player, boolean task) {
        return (task ? taskDrafts : chatDrafts).getOrDefault(player, "");
    }

    public void draft(UUID player, boolean task, String text) {
        if (text == null
                || text.length() > 512
                || text.codePoints().anyMatch(Character::isISOControl)) return;
        Map<UUID, String> drafts = task ? taskDrafts : chatDrafts;
        if (text.isEmpty()) drafts.remove(player);
        else drafts.put(player, text);
    }

    public boolean maySend(UUID player, long now) {
        Long last = sentAt.get(player);
        if (last != null && now - last < 1500) return false;
        sentAt.put(player, now);
        return true;
    }

    public static String question(String raw) {
        if (raw == null
                || raw.length() > 512
                || raw.codePoints().anyMatch(Character::isISOControl)) {
            throw new IllegalArgumentException("问题须为单行，最多 512 字符");
        }
        String text = raw.strip();
        if (text.isEmpty()) throw new IllegalArgumentException("请先输入问题");
        return text;
    }

    public static String clip(String text, int max) {
        if (text == null) return "";
        return text.length() > max ? text.substring(0, max - 1) + "…" : text;
    }

    /** MineUI text is single-line. Wrap plain text before sending; never parse markup/actions. */
    public static List<String> lines(String text) {
        return lines(text, false);
    }

    public static List<String> lines(String text, boolean newest) {
        List<String> out = new ArrayList<>();
        for (String paragraph : text.replace("\r", "").split("\n", -1)) {
            StringBuilder line = new StringBuilder();
            int width = 0;
            for (int cp : paragraph.codePoints().toArray()) {
                int size = cp < 128 ? 1 : 2;
                if (width + size > 50) {
                    out.add(line.toString());
                    line.setLength(0);
                    width = 0;
                }
                line.appendCodePoint(cp);
                width += size;
            }
            out.add(line.isEmpty() ? " " : line.toString());
        }
        return newest
                ? List.copyOf(out.subList(Math.max(0, out.size() - 250), out.size()))
                : out.stream().limit(250).toList();
    }
}
