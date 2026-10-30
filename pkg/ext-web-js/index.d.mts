export class PeerError extends Error { code: string; constructor(code: string, message: string); }
export type Method<I = unknown, O = unknown> = {
  validateInput(value: unknown): I;
  validateOutput(value: unknown): O;
  handle(input: I, context: { signal: AbortSignal; peer: PeerConnection }): Promise<O> | O;
};
export type PeerConnection = {
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
