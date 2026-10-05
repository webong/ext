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
export function loadEngine(addonPath: string): {
 Host: { new(options: HostOptions): Host; open(options: HostOptions, callOptions?: CallOptions): Promise<Host> };
 Guest: { new(options: GuestOptions): Guest };
 service(operation: string, input: unknown): unknown;
 directoryDigest(root: string): Promise<string>;
 verifyArtifacts(manifest: unknown, root: string): Promise<void>;
};
