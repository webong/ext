export interface CallOptions {
 timeout?: number; signal?: AbortSignal;
 /** Receives each progress event of an operation that declares progress, in order,
  * before the call settles. Only `Host.call` reports progress. */
 onProgress?(event: ProgressEvent): void | Promise<void>;
 /** The rate the plugin may report at (zero or absent uses 50 events and 65536 bytes of text per second). */
 progress?: { eventsPerSecond?: number; bytesPerSecond?: number };
}
/** `dropped` counts events lost to the rate limit since the previous one; the last event of a call that lost any is a marker with only `kind: "log"` and `dropped`. */
export interface ProgressEvent { kind: 'progress' | 'log'; stream?: 'out' | 'err'; text?: string; completed?: number; total?: number; dropped?: number }
/** The services a host offers its plugin: the operations its descriptor lists under `reverse`. */
export interface ReverseOptions {
 /** Runs for every request the engine admitted; return false or throw to deny. */
 authorize(request: unknown, context: CallbackContext): void | boolean | Promise<void | boolean>;
 /** Serves an authorized request: a payload, or an error the plugin may see. */
 handle(request: unknown, context: CallbackContext): GuestReply | Promise<GuestReply>;
 /** Milliseconds; zero or absent uses 30000. */
 timeout?: number; maxPayloadBytes?: number; maxInFlight?: number;
}
export const status: Readonly<{ INVALID: 1; DENIED: 2; MISMATCH: 3; NOT_FOUND: 10; PROTOCOL: 16; EXPIRED: 17; EXISTS: 18; MISSING: 19 }>;
export interface CallbackContext { timeout: number; signal: AbortSignal }
export interface GuestReply { payload?: unknown; error?: { code: string; message: string; retryAfterMilliseconds?: number } }
export interface HostOptions {
 executable: string; descriptor: unknown; arguments?: string[]; environment?: string[];
 verify(descriptor: unknown, context: CallbackContext): void | boolean | Promise<void | boolean>;
 authorize(request: unknown, context: CallbackContext): void | boolean | Promise<void | boolean>;
 /** Asynchronous metadata only. Best effort, bounded to 1024 queued events. */
 observe?(event: EngineEvent): void;
 /** Offer reverse-call services to the plugin. */
 reverse?: ReverseOptions;
}
export interface EngineEvent { stage: string; code: string; durationMilliseconds: number; identity: {id: string; revision: string}; contract?: {name: string; version: string}; operation?: string; requestID?: string }
export interface GuestOptions { descriptor: unknown; maxCallDuration?: number; handle(request: unknown, context: CallbackContext): GuestReply | Promise<GuestReply> }
export interface Host {
 start(options?: CallOptions): Promise<null>; invoke(envelope: unknown, options?: CallOptions): Promise<unknown>;
 call(contract: {name: string; version: string}, operation: string, payload?: unknown, options?: CallOptions): Promise<unknown>;
 drain(options?: CallOptions): Promise<void>; close(): void;
}
export interface Guest { descriptor(): Promise<unknown>; invoke(envelope: unknown, options?: CallOptions): Promise<unknown>; close(): void }
export interface ManagerContext { timeout: number; signal: AbortSignal }
export interface InstanceOptions<T> {
 /** 1..4096, including pending factories and retired values. Default 64. */
 capacity?: number;
 /** Reject to refuse a configuration before it is accepted. */
 validate?(config: unknown, context: ManagerContext): void | Promise<void>;
 create(request: { key: string; config: unknown }, context: ManagerContext): T | Promise<T>;
 dispose?(value: T): void | Promise<void>;
}
export interface Lease<T> { readonly value: T; readonly revision: string; release(options?: CallOptions): Promise<void> }
/** Keys and revisions are text without NUL; configuration is any JSON value. */
export interface Instances<T> {
 configure(key: string, revision: string, config?: unknown, options?: CallOptions): Promise<void>;
 acquire(key: string): Promise<Lease<T>>;
 remove(key: string): Promise<void>;
 /** Stops admission, then waits for leases to be released. Rejects with status 6 on timeout; release leases and call again. */
 close(options?: CallOptions): Promise<void>;
}
export interface StreamHandle { read(limit: number, context: ManagerContext): { items: unknown[]; done: boolean } | Promise<{ items: unknown[]; done: boolean }>; close?(): void | Promise<void> }
export interface StreamOptions { capacity?: number; maxAge?: number; open(parameters: unknown, context: ManagerContext): StreamHandle | Promise<StreamHandle> }
export interface Streams {
 open(scope: string, parameters?: unknown, options?: CallOptions): Promise<string>;
 /** Sequences start at 1 and each read must use the previous one plus one. Limit is 1..256. */
 read(scope: string, id: string, sequence: number, limit: number, options?: CallOptions): Promise<{ items: unknown[]; done: boolean }>;
 remove(scope: string, id: string): Promise<void>;
 close(options?: CallOptions): Promise<void>;
}
/** Bounds on a package store; zero or absent takes the default (4 GiB, 250000 files, 16 GiB). */
export interface PackageStoreLimits { maxArtifactBytes?: number; maxTreeFiles?: number; maxTreeBytes?: number }
export interface PackageStoreCopyOptions {
 /** Keep the execute bit of a file artifact that has one in the source. Entrypoints and executable files in trees always keep it. */
 preserveExecutable?: boolean;
}
/**
 * The engine's package store. Every call rejects with an Error whose `status` is INVALID (a package, id or
 * directory that is not valid, or an artifact over a limit), MISMATCH (a digest that differs, or an upgrade
 * that changes the id or contracts), EXISTS (an install over an installed id) or NOT_FOUND. A missing root is
 * an empty store, made by the first install. Calls on one store are serialized; several processes mutating one
 * root are the caller's to serialize.
 */
export interface PackageStore {
 /** Copies the package in the directory `source`, never over an installed id; resolves with the installed plugin.json, as the source had it. */
 install(source: string, options?: PackageStoreCopyOptions): Promise<string>;
 /** Replaces an installed package with a new revision of the same id and contracts. */
 upgrade(source: string, options?: PackageStoreCopyOptions): Promise<string>;
 /** Installs or replaces the package of the source's id, whatever its contracts. */
 replace(source: string, options?: PackageStoreCopyOptions): Promise<string>;
 remove(id: string): Promise<void>;
 /** Reads and verifies one installed package; resolves with its plugin.json. */
 load(id: string): Promise<string>;
 /** The installed ids in byte order, without reading any package. */
 ids(): Promise<string[]>;
 /** Reads and verifies every package. Rejects with the status of the first entry that is not a package directory and of the first package that does not verify. */
 list(): Promise<PackageRecord[]>;
 /** Checks every entry and does not stop at a bad one; each `status` is the engine status verifying it gave (0 verified). Runs nothing and changes nothing. */
 verify(): Promise<PackageCheck[]>;
 /** Reads only the manifests and checks them against their directory names. No artifact is read or hashed, so THE RESULT IS UNVERIFIED and must not be launched or trusted as it stands. */
 listUnverified(): Promise<UnverifiedListing>;
 close(): void;
}
/** One installed package in a report: the entry's id, its directory (the root joined with the entry's name, cleaned), the position of the root that serves it (0 for a store) and its plugin.json as a value. */
export interface PackageRecord { id: string; directory: string; layer: number; manifest: unknown }
/** One entry checked by `verify`, or a problem found by `listUnverified`. */
export interface PackageCheck { id: string; directory: string; layer: number; status: number }
export interface UnverifiedListing { packages: PackageRecord[]; problems: PackageCheck[] }
/**
 * A read-only view over several roots in priority order, highest first, such as a user root before a system
 * root. An id is served from the first root that holds it; a copy in a lower root is shadowed and never read.
 * If the winning copy does not verify, `load` and `list` reject instead of falling back to a lower one. The
 * view never writes, so a system root is never created or modified through it. Roots are compared as cleaned
 * paths: a repeated root throws an Error whose `status` is INVALID from the constructor.
 */
export interface LayeredPackageStore {
 /** Reads and verifies the package from the first root that holds `id`; resolves with its plugin.json and the position of that root. Rejects with NOT_FOUND when no root holds it. */
 load(id: string): Promise<{ manifest: string; layer: number }>;
 list(): Promise<PackageRecord[]>;
 verify(): Promise<PackageCheck[]>;
 listUnverified(): Promise<UnverifiedListing>;
 close(): void;
}
export function loadEngine(addonPath: string): {
 Host: { new(options: HostOptions): Host; open(options: HostOptions, callOptions?: CallOptions): Promise<Host> };
 Guest: { new(options: GuestOptions): Guest };
 Instances: { new<T>(options: InstanceOptions<T>): Instances<T> };
 Streams: { new(options: StreamOptions): Streams };
 PackageStore: { new(root: string, limits?: PackageStoreLimits): PackageStore };
 LayeredPackageStore: { new(roots: string[], limits?: PackageStoreLimits): LayeredPackageStore };
 service(operation: string, input: unknown): unknown;
 /** Each throws an Error whose `status` is INVALID, DENIED or EXPIRED when the endpoint is not admitted. */
 endpoint: { check(endpoint: unknown): void; validate(endpoint: unknown, policy?: { schemes?: string[]; maxLifetimeMilliseconds?: number; now?: string }): void };
 directoryDigest(root: string): Promise<string>;
 verifyArtifacts(manifest: unknown, root: string): Promise<void>;
};
