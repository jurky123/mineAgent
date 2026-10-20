package com.mineagent.paper;

import static org.junit.jupiter.api.Assertions.*;

import com.sk89q.worldedit.WorldEditException;
import com.sk89q.worldedit.entity.BaseEntity;
import com.sk89q.worldedit.entity.Entity;
import com.sk89q.worldedit.extent.Extent;
import com.sk89q.worldedit.extent.NullExtent;
import com.sk89q.worldedit.math.BlockVector3;
import com.sk89q.worldedit.regions.Region;
import com.sk89q.worldedit.util.Location;
import com.sk89q.worldedit.world.biome.BiomeType;
import com.sk89q.worldedit.world.block.BlockStateHolder;

import org.junit.jupiter.api.Test;

import java.util.List;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;

class SpawnProtectedExtentTest {
    private final AtomicReference<SpawnProtectedExtent.Area> area =
            new AtomicReference<>(new SpawnProtectedExtent.Area("world", 158, -41, 512));

    @Test
    void boundaryIncludesAllHeightsAndIsScopedToTheWorld() throws Exception {
        var underlying = new RecordingExtent();
        var guarded = new SpawnProtectedExtent(underlying, "world", area::get, ignored -> {});
        for (int y : new int[] {Integer.MIN_VALUE, -64, 320, Integer.MAX_VALUE}) {
            assertThrows(
                    WorldEditException.class,
                    () -> guarded.setBlock(BlockVector3.at(-354, y, -553), null));
            assertThrows(
                    WorldEditException.class,
                    () -> guarded.setBlock(BlockVector3.at(670, y, 471), null));
        }
        assertEquals(0, underlying.blocks);
        assertTrue(guarded.setBlock(BlockVector3.at(671, 75, 471), null));
        assertTrue(guarded.setBlock(BlockVector3.at(-355, 75, -553), null));
        assertTrue(guarded.setBlock(BlockVector3.at(0, 75, 472), null));
        var other = new SpawnProtectedExtent(underlying, "world_nether", area::get, ignored -> {});
        assertTrue(other.setBlock(BlockVector3.at(158, 75, -41), null));
        assertEquals(4, underlying.blocks);
        assertFalse(guarded.setBiome(BlockVector3.at(671, 75, 0), null));
        assertTrue(guarded.setBiome(BlockVector3.at(672, 75, 0), null));
        assertEquals(1, underlying.biomes);
    }

    @Test
    void crossingSelectionCannotWriteInsideAndUnknownConfigurationFailsClosed() throws Exception {
        var underlying = new RecordingExtent();
        var warnings = new AtomicInteger();
        var guarded =
                new SpawnProtectedExtent(
                        underlying, "world", area::get, ignored -> warnings.incrementAndGet());
        assertTrue(guarded.setBlock(BlockVector3.at(671, 75, 0), null));
        assertThrows(
                WorldEditException.class,
                () -> guarded.setBlock(BlockVector3.at(670, 75, 0), null));
        assertThrows(
                WorldEditException.class,
                () -> guarded.setBlock(BlockVector3.at(158, 75, 0), null));
        assertEquals(1, underlying.blocks);
        assertEquals(1, warnings.get());
        area.set(null);
        assertThrows(
                WorldEditException.class,
                () -> guarded.setBlock(BlockVector3.at(10000, 75, 0), null));
        assertEquals(1, underlying.blocks);
    }

    @Test
    void entityClipboardAndHistoryCannotCreateRemoveOrMoveIntoProtectedArea() {
        var underlying = new RecordingExtent();
        var guarded = new SpawnProtectedExtent(underlying, "world", area::get, ignored -> {});
        Location inside = new Location(underlying, 158, 75, -41);
        Location outside = new Location(underlying, 671, 75, 0);
        assertNull(guarded.createEntity(inside, null));
        Entity entity = guarded.createEntity(outside, null);
        assertNotNull(entity);
        assertFalse(entity.setLocation(inside));
        assertEquals(671, entity.getLocation().getBlockX());
        // A spawn change must also protect entities held by existing history.
        area.set(new SpawnProtectedExtent.Area("world", 671, 0, 512));
        assertFalse(entity.remove());
        assertFalse(guarded.getEntities().getFirst().remove());
        assertFalse(guarded.getEntities((Region) null).getFirst().remove());
        assertEquals(0, underlying.entity.removed);
        assertFalse(guarded.setBiome(BlockVector3.at(671, 75, 0), null));
        assertEquals(0, underlying.biomes);
    }

    private static class RecordingExtent extends NullExtent {
        int blocks, biomes;
        final DummyEntity entity = new DummyEntity(this);

        @Override
        public <T extends BlockStateHolder<T>> boolean setBlock(BlockVector3 pos, T block) {
            blocks++;
            return true;
        }

        @Override
        public boolean setBiome(BlockVector3 pos, BiomeType biome) {
            biomes++;
            return true;
        }

        @Override
        public Entity createEntity(Location location, BaseEntity state) {
            entity.location = location;
            return entity;
        }

        @Override
        public List<Entity> getEntities() {
            return List.of(entity);
        }

        @Override
        public List<Entity> getEntities(Region region) {
            return getEntities();
        }
    }

    private static class DummyEntity implements Entity {
        final Extent extent;
        Location location;
        int removed;

        DummyEntity(Extent extent) {
            this.extent = extent;
        }

        public BaseEntity getState() {
            return null;
        }

        public Location getLocation() {
            return location;
        }

        public boolean setLocation(Location location) {
            this.location = location;
            return true;
        }

        public Extent getExtent() {
            return extent;
        }

        public boolean remove() {
            removed++;
            return true;
        }

        public <T> T getFacet(Class<? extends T> type) {
            return null;
        }
    }
}
