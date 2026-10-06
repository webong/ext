export interface CallOptions { timeout?: number; signal?: AbortSignal }
export interface CallbackContext { timeout: number; signal: AbortSignal }
export interface GuestReply { payload?: unknown; error?: { code: string; message: string; retryAfterMilliseconds?: number } }
export interface HostOptions {
 executable: string; descriptor: unknown; arguments?: string[]; environment?: string[];
 verify(descriptor: unknown, context: CallbackContext): void | boolean | Promise<void | boolean>;
 authorize(request: unknown, context: CallbackContext): void | boolean | Promise<void | boolean>;
 /** Asynchronous metadata only. Best effort, bounded to 1024 queued events. */
 observe?(event: EngineEvent): void;
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
export function loadEngine(addonPath: string): {
 Host: { new(options: HostOptions): Host; open(options: HostOptions, callOptions?: CallOptions): Promise<Host> };
 Guest: { new(options: GuestOptions): Guest };
 Instances: { new<T>(options: InstanceOptions<T>): Instances<T> };
 Streams: { new(options: StreamOptions): Streams };
 service(operation: string, input: unknown): unknown;
 directoryDigest(root: string): Promise<string>;
 verifyArtifacts(manifest: unknown, root: string): Promise<void>;
};
