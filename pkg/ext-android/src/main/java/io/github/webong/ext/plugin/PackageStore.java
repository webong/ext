package io.github.webong.ext.plugin;

import java.nio.ByteBuffer;
import java.nio.CharBuffer;
import java.nio.charset.CharacterCodingException;
import java.nio.charset.CodingErrorAction;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Objects;
import java.util.concurrent.locks.ReadWriteLock;
import java.util.concurrent.locks.ReentrantReadWriteLock;

/**
 * The package store of the shared C engine: install, upgrade, replace, remove and load
 * reviewed {@code ext.package/v1} packages under a root the application supplies (see
 * {@code ext_store_*} in lib/plugin/include/ext_host.h). A package is its {@code plugin.json}
 * manifest and the artifacts it lists, in a directory named by the package id with '/' encoded
 * as "%2f". The store copies and re-verifies content; it never runs it or grants authority, and
 * it has no default location. A missing root is an empty store, made (owner-only) by the first
 * install. Calls on one store are serialized by the engine and the class is safe to share between
 * threads; several processes mutating one root are the application's to serialize.
 *
 * <p>A failure is a {@link GuestException} whose {@link GuestException#status() status} is one of
 * the constants here: {@link #INVALID} for a package, id or directory that is not valid, a
 * directory that is not a package, or an artifact over a limit; {@link #MISMATCH} for an artifact
 * whose digest differs, a directory that does not match its manifest, or an upgrade that changes
 * the id or the contracts; {@link #EXISTS} for an install over an installed id; {@link #NOT_FOUND}
 * for an id that is not installed; {@link #MISSING} for a source directory or a file it lists that
 * is not there.
 *
 * <p>The manifest a call returns is the installed {@code plugin.json}, byte for byte. The whole-root
 * reports ({@link #list()}, {@link #verify()}, {@link #listUnverified()}) are the engine's JSON as
 * UTF-8, as {@link #load} returns the manifest, so a caller reads them with its own JSON library;
 * the shapes are those of {@code ext_store_list} in lib/plugin/include/ext_host.h.
 */
public final class PackageStore implements AutoCloseable {
    /** A package, id or directory that is not valid, or an artifact over a limit. */
    public static final int INVALID = 1;
    /** An artifact whose digest differs, a directory that is not what its name says, or an upgrade that changes the id or contracts. */
    public static final int MISMATCH = 3;
    /** The store was closed. */
    public static final int CLOSED = 5;
    /** The operating system refused a read or a write. */
    public static final int IO = 7;
    /** The engine could not allocate. */
    public static final int NOMEM = 8;
    /** An id that is not installed (or, in a layered view, in no root). */
    public static final int NOT_FOUND = 10;
    /** An install over an installed id. */
    public static final int EXISTS = 18;
    /** A source directory, its manifest or an artifact it lists is not there. */
    public static final int MISSING = 19;

    /** The engine keeps the owner-execute bit of a file artifact that has any execute bit in the source. */
    private static final int PRESERVE_EXECUTABLE = 1;

    private final ReadWriteLock lock = new ReentrantReadWriteLock();
    private long handle;

    /** Opens a store at {@code root}, which need not exist yet, with the engine's default limits. */
    public PackageStore(String root) throws GuestException {
        this(root, StoreLimits.DEFAULT);
    }

    /** Opens a store at {@code root}, which need not exist yet. An empty root is {@link #INVALID}. */
    public PackageStore(String root, StoreLimits limits) throws GuestException {
        Objects.requireNonNull(limits, "limits");
        handle = NativeEngine.storeOpen(text(Objects.requireNonNull(root, "root")),
                limits.maxArtifactBytes(), limits.maxTreeFiles(), limits.maxTreeBytes());
    }

    /**
     * Copies the package in the directory {@code source} into the store, never over an installed
     * id, and returns the installed {@code plugin.json}, byte for byte as the source had it. A
     * file artifact keeps no execute bit unless it is an entrypoint; see {@link #install(String,
     * boolean)}.
     */
    public byte[] install(String source) throws GuestException {
        return install(source, false);
    }

    /**
     * As {@link #install(String)}. With {@code preserveExecutable} a file artifact that has any
     * execute bit in the source keeps the owner-execute bit; the files of a tree always do.
     */
    public byte[] install(String source, boolean preserveExecutable) throws GuestException {
        return put(NativeEngine.PUT_INSTALL, source, preserveExecutable);
    }

    /**
     * Replaces an installed package with a new revision of the same id and contracts, and returns
     * the installed {@code plugin.json}. The previous installation survives any failure.
     */
    public byte[] upgrade(String source) throws GuestException {
        return upgrade(source, false);
    }

    /** As {@link #upgrade(String)}; see {@link #install(String, boolean)} for {@code preserveExecutable}. */
    public byte[] upgrade(String source, boolean preserveExecutable) throws GuestException {
        return put(NativeEngine.PUT_UPGRADE, source, preserveExecutable);
    }

    /**
     * Installs or replaces the package of the source's id, whatever its contracts, and returns the
     * installed {@code plugin.json}.
     */
    public byte[] replace(String source) throws GuestException {
        return replace(source, false);
    }

    /** As {@link #replace(String)}; see {@link #install(String, boolean)} for {@code preserveExecutable}. */
    public byte[] replace(String source, boolean preserveExecutable) throws GuestException {
        return put(NativeEngine.PUT_REPLACE, source, preserveExecutable);
    }

    /** Deletes one installed package. */
    public void remove(String id) throws GuestException {
        byte[] name = text(Objects.requireNonNull(id, "id"));
        lock.readLock().lock();
        try {
            NativeEngine.storeRemove(live(), name);
        } finally {
            lock.readLock().unlock();
        }
    }

    /** Reads and verifies one installed package and returns its {@code plugin.json}. */
    public byte[] load(String id) throws GuestException {
        byte[] name = text(Objects.requireNonNull(id, "id"));
        lock.readLock().lock();
        try {
            return NativeEngine.storeLoad(live(), name);
        } finally {
            lock.readLock().unlock();
        }
    }

    /**
     * The ids of the installed packages in byte order, read without verifying any package.
     * Staging directories (a leading ".") are skipped; any other entry that is not a package
     * directory is {@link #INVALID}.
     */
    public List<String> ids() throws GuestException {
        return parseIds(report(NativeEngine.REPORT_IDS));
    }

    /**
     * Reads and verifies every package and returns a JSON array of {@code {id, directory, layer,
     * manifest}} sorted by id, the manifest being {@code plugin.json} as an embedded value. It
     * fails with the status of the first entry that is not a package directory and of the first
     * package that does not verify. A missing root is empty and is not made.
     */
    public byte[] list() throws GuestException {
        return report(NativeEngine.REPORT_LIST);
    }

    /**
     * Checks every entry without stopping at a bad one and returns a JSON array of {@code {id,
     * directory, layer, status}}, where {@code status} is the engine status that verifying it gave
     * (0 for verified). It runs nothing and changes nothing.
     */
    public byte[] verify() throws GuestException {
        return report(NativeEngine.REPORT_VERIFY);
    }

    /**
     * Reads only the manifests and checks them against their directory names; no artifact is read
     * or hashed, so <b>the result is unverified</b> and must not be launched or trusted as it
     * stands. Returns {@code {"packages": [...], "problems": [...]}} as JSON; a package that cannot
     * be read does not hide the others.
     */
    public byte[] listUnverified() throws GuestException {
        return report(NativeEngine.REPORT_UNVERIFIED);
    }

    /** Waits for the calls in flight, then releases the store. Idempotent; later calls fail with {@link #CLOSED}. */
    @Override
    public void close() {
        lock.writeLock().lock();
        try {
            if (handle != 0) {
                NativeEngine.storeClose(handle);
                handle = 0;
            }
        } finally {
            lock.writeLock().unlock();
        }
    }

    private byte[] put(int how, String source, boolean preserveExecutable) throws GuestException {
        byte[] path = text(Objects.requireNonNull(source, "source"));
        lock.readLock().lock();
        try {
            return NativeEngine.storePut(live(), how, path, preserveExecutable ? PRESERVE_EXECUTABLE : 0);
        } finally {
            lock.readLock().unlock();
        }
    }

    private byte[] report(int kind) throws GuestException {
        lock.readLock().lock();
        try {
            return NativeEngine.storeReport(live(), kind);
        } finally {
            lock.readLock().unlock();
        }
    }

    private long live() throws GuestException {
        if (handle == 0) {
            throw new GuestException(CLOSED);
        }
        return handle;
    }

    /** A JSON array of the ids the engine listed. Ids hold only [a-z0-9._/-], so nothing in them is escaped. */
    private static List<String> parseIds(byte[] json) throws GuestException {
        List<String> ids = new ArrayList<>();
        try {
            if (!(Json.parse(json) instanceof List<?> items)) {
                throw new GuestException(INVALID);
            }
            for (Object item : items) {
                String id = Json.string(item);
                if (id == null) {
                    throw new GuestException(INVALID);
                }
                ids.add(id);
            }
        } catch (IllegalArgumentException e) {
            throw new GuestException(INVALID);
        }
        return Collections.unmodifiableList(ids);
    }

    /**
     * A path or id as the UTF-8 bytes the engine reads. A string that is not well formed UTF-16 (a
     * lone surrogate) has no such spelling and is invalid, where {@code getBytes} would silently
     * name another path.
     */
    static byte[] text(String value) throws GuestException {
        try {
            ByteBuffer encoded = StandardCharsets.UTF_8.newEncoder()
                    .onMalformedInput(CodingErrorAction.REPORT)
                    .onUnmappableCharacter(CodingErrorAction.REPORT)
                    .encode(CharBuffer.wrap(value));
            byte[] bytes = new byte[encoded.remaining()];
            encoded.get(bytes);
            return bytes;
        } catch (CharacterCodingException e) {
            throw new GuestException(INVALID);
        }
    }
}
