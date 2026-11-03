export class PeerError extends Error { code: string; constructor(code: string, message: string); }
export type Method<I = unknown, O = unknown> = {
  validateInput(value: unknown): I;
  validateOutput(value: unknown): O;
  handle(input: I, context: { signal: AbortSignal; peer: PeerConnection }): Promise<O> | O;
};
export type PeerConnection = {
  /** Browser-supplied extension ID on an admitted host connection. */
  id?: string;
  call<O = unknown>(method: string, value: unknown, options?: { signal?: AbortSignal; timeoutMs?: number; validateOutput?: (value: unknown) => O }): Promise<O>;
  event(value: unknown): void;
  state(value: unknown): void;
};
export type Sender = Readonly<{ id: string; origin?: string; url?: string; tabId?: number; frameId?: number }>;
export type Runtime = { connect(extensionId: string, options: { name: string }): any; onConnectExternal: { addListener(listener: (port: any) => void): void; removeListener(listener: (port: any) => void): void } };
export function createHost(options: {
  runtime?: Runtime;
  methods?: Record<string, Method<any, any>>;
  admit(sender: Sender): boolean | Promise<boolean>;
  authorize?(sender: Sender, method: string): void | Promise<void>;
  onEvent?(sender: Sender, value: unknown): void;
  onState?(sender: Sender, value: unknown): void;
  onChange?(peers: { id: string; methods: string[] }[]): void;
  timeoutMs?: number;
}): { start(): void; stop(): void; peers(): { id: string; methods: string[] }[]; call<O = unknown>(id: string, method: string, value: unknown, options?: { signal?: AbortSignal; timeoutMs?: number; validateOutput?: (value: unknown) => O }): Promise<O>; event(id: string, value: unknown): void };
export function createGuest(options: {
  runtime?: Runtime;
  hostId: string;
  methods?: Record<string, Method<any, any>>;
  authorize?(method: string): void | Promise<void>;
  onEvent?(value: unknown): void;
  onState?(value: unknown): void;
  onChange?(connected: boolean): void;
  timeoutMs?: number;
  reconnectMs?: number;
}): { start(): void; stop(): void; connected(): boolean; call<O = unknown>(method: string, value: unknown, options?: { signal?: AbortSignal; timeoutMs?: number; validateOutput?: (value: unknown) => O }): Promise<O>; event(value: unknown): void; state(value: unknown): void };
export function discoverExtension(match: (extension: { id: string; name: string; enabled: boolean }) => boolean, management?: { getAll(): Promise<{ id: string; name: string; enabled: boolean }[]> }): Promise<string | null>;
export function createSpoke(options: {
  runtime?: Runtime;
  hostId?: string;
  discover?: () => Promise<string | null>;
  name: string;
  initialState?: Record<string, unknown>;
  uiPath?: string;
  methods?: Record<string, Method<any, any>>;
  authorize?(method: string): void | Promise<void>;
  onEvent?(value: unknown): void;
  onState?(value: unknown): void;
  onChange?(connected: boolean, hostId: string | null): void;
  timeoutMs?: number;
  reconnectMs?: number;
  discoveryMs?: number;
}): {
  start(): void; stop(): void; connected(): boolean; hostId(): string | null;
  register(): boolean; updateState(patch: Record<string, unknown>): boolean;
  state(): Record<string, unknown>; event(value: unknown): boolean;
  call<O = unknown>(method: string, value: unknown, options?: { signal?: AbortSignal; timeoutMs?: number; validateOutput?: (value: unknown) => O }): Promise<O>;
  refresh(): Promise<void>;
};
export type SpokeInfo = { id: string; name: string; uiPath?: string; state: Record<string, unknown> };
export function createHub(options: {
  runtime?: Runtime;
  admit(sender: Sender): boolean | Promise<boolean>;
  methods?: Record<string, Method<any, any>>;
  authorize?(sender: Sender, method: string): void | Promise<void>;
  onEvent?(sender: Sender, value: unknown): void;
  onSpokesChange?(spokes: SpokeInfo[]): void;
  timeoutMs?: number;
}): {
  start(): void; stop(): void; spokes(): SpokeInfo[];
  call<O = unknown>(id: string, method: string, value: unknown, options?: { signal?: AbortSignal; timeoutMs?: number; validateOutput?: (value: unknown) => O }): Promise<O>;
  event(id: string, value: unknown): void;
};
