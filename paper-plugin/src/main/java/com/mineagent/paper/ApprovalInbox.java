package com.mineagent.paper;

import java.util.Comparator;
import java.util.List;
import java.util.concurrent.ConcurrentHashMap;
import java.util.function.LongSupplier;

/** Bounded local projection; the backend remains authoritative about approval results. */
public final class ApprovalInbox {
    public record Pending(
            String id,
            String tool,
            String requester,
            String prompt,
            long expiresAt,
            long generation) {}

    private final ConcurrentHashMap<String, Pending> entries = new ConcurrentHashMap<>();
    private final LongSupplier clock;
    private final LongSupplier generation;

    public ApprovalInbox(LongSupplier clock, LongSupplier generation) {
        this.clock = clock;
        this.generation = generation;
    }

    public void add(String id, String tool, String requester, String prompt, long timeoutMs) {
        list();
        if (id == null || !id.matches("[A-Za-z0-9_-]{1,100}") || entries.size() >= 64) return;
        long ttl = timeoutMs > 0 ? Math.min(timeoutMs, 300_000L) : 180_000L;
        entries.put(
                id,
                new Pending(
                        id,
                        clip(tool, 100),
                        clip(requester, 100),
                        clip(prompt, 2000),
                        clock.getAsLong() + ttl,
                        generation.getAsLong()));
    }

    public List<Pending> list() {
        long now = clock.getAsLong(), current = generation.getAsLong();
        entries.values().removeIf(p -> p.expiresAt() <= now || p.generation() != current);
        return entries.values().stream()
                .sorted(Comparator.comparingLong(Pending::expiresAt).thenComparing(Pending::id))
                .toList();
    }

    public Pending get(String id) {
        list();
        return entries.get(id);
    }

    public void remove(String id) {
        entries.remove(id);
    }

    private static String clip(String value, int max) {
        if (value == null) return "";
        return value.length() > max ? value.substring(0, max) : value;
    }
}
