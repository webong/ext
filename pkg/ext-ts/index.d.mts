export const API_VERSION: 'ext.plugin/v1';
export const MAX_FRAME_BYTES: number;
export const MAX_PROGRESS_TEXT: number;
export const MAX_RETRY_AFTER_MS: number;
export interface Identity {
    id: string;
    revision: string;
    version?: string;
}
export interface ContractRef {
    name: string;
    version: string;
}
export interface Operation {
    name: string;
    surface?: string;
    /** The operation reports progress events while it runs. Not valid on a reverse operation. */
    progress?: boolean;
}
export interface Descriptor {
    apiVersion: typeof API_VERSION;
    identity: Identity;
    contracts: (ContractRef & {
        operations: Operation[];
    })[];
    /** Host operations the plugin may call back into. */
    reverse?: (ContractRef & {
        operations: Operation[];
    })[];
}
export interface ProgressLimits {
    eventsPerSecond?: number;
    bytesPerSecond?: number;
}
export interface Request {
    apiVersion: typeof API_VERSION;
    id: string;
    plugin: Identity;
    contract: ContractRef;
    operation: string;
    surface?: string;
    deadline: string;
    payload?: unknown;
    /** The host's rate limits for progress events; present when it wants them. */
    progress?: ProgressLimits;
    /** The call that issued this one; empty for a session-scoped request. */
    parent?: string;
}
export interface Response {
    apiVersion: typeof API_VERSION;
    id: string;
    payload?: unknown;
    error?: {
        code: string;
        message: string;
        retryAfterMilliseconds?: number;
    };
}
export interface Endpoint {
    handshake(signal?: AbortSignal): Promise<Descriptor>;
    invoke(request: Request, signal?: AbortSignal): Promise<Response>;
}
export interface Backend extends Endpoint {
    close(): Promise<void> | void;
}
export interface Caller {
    call(ref: ContractRef, operation: string, payload?: unknown, signal?: AbortSignal): Promise<unknown>;
}
export interface IO {
    read(signal?: AbortSignal): Promise<Uint8Array | null>;
    write(bytes: Uint8Array, signal?: AbortSignal): Promise<void>;
    close(): Promise<void> | void;
}
export interface Codec<T> {
    parse(value: unknown): T;
}
export interface Method<I, O> {
    contract: ContractRef;
    operation: Operation;
    input: Codec<I>;
    output: Codec<O>;
}
export type Authorize = (request: Request, signal: AbortSignal) => Promise<void> | void;
export interface HostOptions {
    verify(descriptor: Descriptor, signal: AbortSignal): Promise<void> | void;
    connect(descriptor: Descriptor, signal: AbortSignal): Promise<Backend> | Backend;
    authorize: Authorize;
    timeoutMs?: number;
    signal?: AbortSignal;
}
export function decodeJSON(bytes: string | Uint8Array): unknown;
export function validateDescriptor(descriptor: Descriptor): void;
export function validateRequest(descriptor: Descriptor, request: Request): void;
export function lookup(descriptor: Descriptor, ref: ContractRef, operation: string): Operation;
export function lookupReverse(descriptor: Descriptor, ref: ContractRef, operation: string): Operation;
export function matchHandshake(expected: Descriptor, actual: Descriptor): void;
export class RemoteError extends Error {
    constructor(code: string, message: string, retryAfterMilliseconds?: number);
    code: string;
    retryAfterMilliseconds: number;
}
/** A frame operation failed locally. `code` is `unsupported` (no reporter or no caller
 * on this call), `invalid` (the event breaks the rules; nothing was sent), `closed`
 * (the call is over or the connection ended), `deadline`, `canceled`, or `protocol`
 * (the host broke the framing rules). */
export class FrameError extends Error {
    constructor(code: 'unsupported' | 'invalid' | 'closed' | 'deadline' | 'canceled' | 'protocol', message?: string);
    code: 'unsupported' | 'invalid' | 'closed' | 'deadline' | 'canceled' | 'protocol';
}
export interface ProgressEvent {
    kind: 'progress' | 'log';
    /** Only a `log` event has a stream. */
    stream?: '' | 'out' | 'err';
    /** At most MAX_PROGRESS_TEXT bytes of UTF-8. */
    text?: string;
    completed?: number;
    total?: number;
}
export interface ReverseCaller {
    call(ref: ContractRef, operation: string, payload?: unknown, signal?: AbortSignal): Promise<unknown>;
    /** Several reverse calls may be in flight at once. */
    readonly concurrent: boolean;
}
/** What a call can do beyond returning a result. On a transport with no frames (an
 * in-process call, the C ABI) `progress` fails with `unsupported`, and the callers are null. */
export interface CallFrames {
    /** Reports an event: `sent`, or `dropped` when the rate limit drops it. */
    progress(event: ProgressEvent): Promise<'sent' | 'dropped'>;
    /** The limits in force, or null when the operation reports no progress. */
    readonly progressLimits: { eventsPerSecond: number; bytesPerSecond: number } | null;
    /** Reverse calls scoped to this call: bound by its deadline. */
    readonly reverse: ReverseCaller | null;
    /** Reverse calls scoped to the session: not tied to a call. */
    readonly sessionReverse: ReverseCaller | null;
    finish(): Promise<void>;
}
export interface FrameProgress {
    parent: string;
    event: { kind: string; stream: string; text: string; completed: number; total: number; dropped: number };
}
export function validateProgressFrame(value: unknown): FrameProgress;
export function validateReverseRequest(value: unknown): void;
export function validateReverseResponse(value: unknown, id: string): { payload: unknown } | { error: { code: string; message: string; retryAfterMilliseconds: number } };
export function splitProgressText(text: string): string[];
/** The sender's rate limiter: a token bucket counted in events and bytes of text with a
 * burst of twice the rate. Time is a monotonic clock in nanoseconds, given by the caller. */
export class Limiter {
    constructor(limits?: ProgressLimits, nowNs?: number);
    /** The number of events dropped since the previous one that passed, or null when the rate limit drops this one. */
    admit(textBytes: number, nowNs: number): number | null;
    /** The drops not yet reported, reset to zero. */
    pending(): number;
}
export class Guest implements Endpoint {
    constructor(descriptor: Descriptor, options: {
        handler(request: Request, signal: AbortSignal, frames: CallFrames): Promise<unknown> | unknown;
        timeoutMs?: number;
    });
    handshake(signal?: AbortSignal): Promise<Descriptor>;
    invoke(request: Request, signal?: AbortSignal, frames?: CallFrames): Promise<Response>;
}
export class Session implements Caller {
    private constructor();
    static open(descriptor: Descriptor, options: HostOptions): Promise<Session>;
    readonly state: 'ready' | 'draining' | 'closed' | 'failed';
    readonly descriptor: Descriptor;
    call(ref: ContractRef, operation: string, payload?: unknown, signal?: AbortSignal): Promise<unknown>;
    close(signal?: AbortSignal): Promise<void>;
    abort(): Promise<void>;
}
export class Registry {
    constructor(identity: Identity);
    register<I, O>(method: Method<I, O>, handler: (input: I, signal: AbortSignal, request: Request, frames: CallFrames) => Promise<O> | O): this;
    /** Declares host operations the plugin may call (`frames.reverse`). */
    declareReverse(ref: ContractRef, operations: Operation[]): this;
    readonly descriptor: Descriptor;
    guest(options: {
        authorize: Authorize;
        timeoutMs?: number;
    }): Guest;
}
export function call<I, O>(caller: Caller, method: Method<I, O>, input: I, signal?: AbortSignal): Promise<O>;
export class JSONLineBackend implements Backend {
    constructor(io: IO);
    handshake(signal?: AbortSignal): Promise<Descriptor>;
    invoke(request: Request, signal?: AbortSignal): Promise<Response>;
    close(): Promise<void>;
}
export function serveGuest(io: IO, guest: Endpoint & { invoke(request: Request, signal?: AbortSignal, frames?: CallFrames): Promise<Response> }, signal?: AbortSignal): Promise<void>;
export interface Schema {
    type: 'object' | 'array' | 'string' | 'number' | 'integer' | 'boolean' | 'null';
    properties?: Record<string, Schema>;
    required?: string[];
    additionalProperties?: boolean;
    items?: Schema;
    enum?: string[];
    minimum?: number;
    maximum?: number;
    minLength?: number;
    maxLength?: number;
    minItems?: number;
    maxItems?: number;
}
export function schemaCodec<T = unknown>(schema: Schema): Codec<T>;
export const healthMethod: Method<Record<string, never>, {
    status: 'healthy' | 'degraded' | 'unhealthy';
    ready: boolean;
}>;
export const configurationMethod: Method<{
    instance: string;
    revision: string;
    values: unknown;
}, {
    instance: string;
    revision: string;
}>;
export function openStream(caller: Caller, parameters: unknown, signal?: AbortSignal): Promise<{
    read(limit?: number, signal?: AbortSignal): Promise<{
        items: unknown[];
        done: boolean;
    }>;
    close(signal?: AbortSignal): Promise<void>;
}>;
