package com.mineagent.paper;

import com.sk89q.worldedit.WorldEditException;
import com.sk89q.worldedit.entity.BaseEntity;
import com.sk89q.worldedit.entity.Entity;
import com.sk89q.worldedit.extent.AbstractDelegateExtent;
import com.sk89q.worldedit.extent.Extent;
import com.sk89q.worldedit.math.BlockVector3;
import com.sk89q.worldedit.regions.Region;
import com.sk89q.worldedit.util.Location;
import com.sk89q.worldedit.world.biome.BiomeType;
import com.sk89q.worldedit.world.block.BlockStateHolder;

import java.util.List;
import java.util.function.Consumer;
import java.util.function.Supplier;

// WorldEdit's actual write boundary, independent of command spelling, selection,
// clipboard transforms, batching, or the model's interpretation of the boundary.
final class SpawnProtectedExtent extends AbstractDelegateExtent {
    record Area(String world, int x, int z, int radius) {
        boolean contains(String worldName, int blockX, int blockZ) {
            return world.equals(worldName)
                    && Math.abs((long) blockX - x) <= radius
                    && Math.abs((long) blockZ - z) <= radius;
        }

        boolean intersects(String worldName, int minX, int minZ, int maxX, int maxZ) {
            return world.equals(worldName)
                    && (long) maxX >= (long) x - radius
                    && (long) minX <= (long) x + radius
                    && (long) maxZ >= (long) z - radius
                    && (long) minZ <= (long) z + radius;
        }
    }

    private final String world;
    private final Supplier<Area> area;
    private final Consumer<String> warning;
    private boolean warned;

    SpawnProtectedExtent(
            Extent extent, String world, Supplier<Area> area, Consumer<String> warning) {
        super(extent);
        this.world = world;
        this.area = area;
        this.warning = warning;
    }

    private boolean protectedAt(int x, int z) {
        Area current = area.get();
        // Unknown spawn/world configuration fails closed for this AI only.
        return current == null || current.contains(world, x, z);
    }

    private void warn() {
        if (!warned) {
            warned = true;
            warning.accept("AI WorldEdit：出生点保护区内禁止修改；区外可能已有部分改动，请核查后再操作。");
        }
    }

    @Override
    public <T extends BlockStateHolder<T>> boolean setBlock(BlockVector3 pos, T block)
            throws WorldEditException {
        if (protectedAt(pos.x(), pos.z())) {
            warn();
            throw new ProtectedEditException();
        }
        return super.setBlock(pos, block);
    }

    @Override
    public boolean setBiome(BlockVector3 pos, BiomeType biome) {
        // Biomes are stored in 4x4x4 cells; a boundary write must not change
        // a cell that overlaps protected blocks.
        int x = Math.floorDiv(pos.x(), 4) * 4, z = Math.floorDiv(pos.z(), 4) * 4;
        Area current = area.get();
        if (current == null || current.intersects(world, x, z, x + 3, z + 3)) {
            warn();
            return false;
        }
        return super.setBiome(pos, biome);
    }

    @Override
    public Entity createEntity(Location pos, BaseEntity entity) {
        if (protectedAt(pos.getBlockX(), pos.getBlockZ())) {
            warn();
            return null;
        }
        Entity created = super.createEntity(pos, entity);
        return created == null ? null : guarded(created);
    }

    @Override
    public List<? extends Entity> getEntities() {
        return super.getEntities().stream().map(this::guarded).toList();
    }

    @Override
    public List<? extends Entity> getEntities(Region region) {
        return super.getEntities(region).stream().map(this::guarded).toList();
    }

    private Entity guarded(Entity delegate) {
        return new Entity() {
            public BaseEntity getState() {
                return delegate.getState();
            }

            public Location getLocation() {
                return delegate.getLocation();
            }

            public Extent getExtent() {
                return SpawnProtectedExtent.this;
            }

            public <T> T getFacet(Class<? extends T> type) {
                return delegate.getFacet(type);
            }

            public boolean remove() {
                Location pos = delegate.getLocation();
                if (protectedAt(pos.getBlockX(), pos.getBlockZ())) {
                    warn();
                    return false;
                }
                return delegate.remove();
            }

            public boolean setLocation(Location target) {
                Location source = delegate.getLocation();
                if (protectedAt(source.getBlockX(), source.getBlockZ())
                        || protectedAt(target.getBlockX(), target.getBlockZ())) {
                    warn();
                    return false;
                }
                return delegate.setLocation(target);
            }
        };
    }

    private static final class ProtectedEditException extends WorldEditException {
        ProtectedEditException() {
            super("AI WorldEdit：禁止修改出生点保护区。");
        }
    }
}
