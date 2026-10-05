export const API_VERSION: 'ctx.plugin/v1';
export const MAX_FRAME_BYTES: number;
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
}
export interface Descriptor {
    apiVersion: typeof API_VERSION;
    identity: Identity;
    contracts: (ContractRef & {
        operations: Operation[];
    })[];
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
export function lookup(descriptor: Descriptor, ref: ContractRef, operation: string): Operation;
export function matchHandshake(expected: Descriptor, actual: Descriptor): void;
export class RemoteError extends Error {
    constructor(code: string, message: string, retryAfterMilliseconds?: number);
    code: string;
    retryAfterMilliseconds: number;
}
export class Guest implements Endpoint {
    constructor(descriptor: Descriptor, options: {
        handler(request: Request, signal: AbortSignal): Promise<unknown> | unknown;
        timeoutMs?: number;
    });
    handshake(signal?: AbortSignal): Promise<Descriptor>;
    invoke(request: Request, signal?: AbortSignal): Promise<Response>;
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
    register<I, O>(method: Method<I, O>, handler: (input: I, signal: AbortSignal, request: Request) => Promise<O> | O): this;
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
export function serveGuest(io: IO, guest: Endpoint, signal?: AbortSignal): Promise<void>;
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
export function openStream(caller: Caller, parameters: unknown, signal?: AbortSignal): Promise<{
    read(limit?: number, signal?: AbortSignal): Promise<{
        items: unknown[];
        done: boolean;
    }>;
    close(signal?: AbortSignal): Promise<void>;
}>;
