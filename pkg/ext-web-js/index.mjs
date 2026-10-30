const VERSION = 'ext.web.peer/v1';
const PORT_NAME = 'ext.web.peer';
const MAX_BYTES = 1024 * 1024;
const encoder = new TextEncoder();

export class PeerError extends Error {
  constructor(code, message) { super(message); this.name = 'PeerError'; this.code = code; }
}

function checkMessage(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value) || value.version !== VERSION || typeof value.type !== 'string') throw new PeerError('protocol', 'Invalid peer message');
  let bytes;
  try { bytes = encoder.encode(JSON.stringify(value)).length; } catch { throw new PeerError('protocol', 'Message is not JSON serializable'); }
  if (bytes > MAX_BYTES) throw new PeerError('too_large', 'Peer message exceeds 1 MiB');
  return value;
}

function descriptor(methods) {
  return Object.entries(methods).map(([name, method]) => {
    if (!/^[a-z][a-z0-9.-]{0,127}$/.test(name) || !method || typeof method.handle !== 'function' || typeof method.validateInput !== 'function' || typeof method.validateOutput !== 'function') throw new PeerError('invalid', `Invalid method: ${name}`);
    return name;
  });
}

function remoteError(error) {
  return error instanceof PeerError ? { code: error.code, message: error.message } : { code: 'operation_failed', message: 'Operation failed' };
}

class Connection {
  constructor(port, methods, authorize, onEvent, onState, onDisconnect, timeoutMs) {
    this.port = port; this.methods = methods; this.authorize = authorize;
    this.onEvent = onEvent; this.onState = onState; this.onDisconnect = onDisconnect;
    this.timeoutMs = timeoutMs; this.pending = new Map(); this.running = new Map();
    this.closed = false; this.ready = false; this.remoteMethods = [];
    this.messageListener = message => { void this.receive(message); };
    this.disconnectListener = () => this.close();
    port.onMessage.addListener(this.messageListener);
    port.onDisconnect.addListener(this.disconnectListener);
  }
  send(message) {
    if (this.closed) throw new PeerError('closed', 'Peer disconnected');
    this.port.postMessage(checkMessage({ version: VERSION, ...message }));
  }
  async receive(raw) {
    let message;
    try { message = checkMessage(raw); } catch { this.close(); return; }
    if (message.type === 'hello') {
      if (this.ready || !Array.isArray(message.methods) || message.methods.some(x => typeof x !== 'string')) { this.close(); return; }
      this.remoteMethods = [...new Set(message.methods)]; this.ready = true;
      this.onState?.(this, 'connected'); return;
    }
    if (!this.ready) { this.close(); return; }
    if (!['result', 'cancel', 'event', 'state', 'call'].includes(message.type)) { this.close(); return; }
    if (message.type === 'result') {
      const pending = this.pending.get(message.id);
      if (!pending) return;
      this.pending.delete(message.id); clearTimeout(pending.timer); pending.signal?.removeEventListener('abort', pending.abort);
      if (message.error) pending.reject(new PeerError(message.error.code ?? 'remote_error', message.error.message ?? 'Remote error'));
      else { try { pending.resolve(pending.validate(message.value)); } catch (error) { pending.reject(error); } }
      return;
    }
    if (message.type === 'cancel') { this.running.get(message.id)?.abort(); return; }
    if (message.type === 'event' || message.type === 'state') {
      try {
        if (message.type === 'event') await this.onEvent?.(this, message.value);
        else await this.onState?.(this, 'state', message.value);
      } catch { /* Consumer callback failures do not alter the peer protocol. */ }
      return;
    }
    if (message.type !== 'call' || typeof message.id !== 'string' || typeof message.method !== 'string' || this.running.has(message.id)) { this.close(); return; }
    const controller = new AbortController(); this.running.set(message.id, controller);
    try {
      const method = this.methods[message.method];
      if (!method) throw new PeerError('unsupported', 'Unknown method');
      await this.authorize?.(this, message.method);
      const input = method.validateInput(message.value);
      const output = await method.handle(input, { signal: controller.signal, peer: this });
      if (!controller.signal.aborted) this.send({ type: 'result', id: message.id, value: method.validateOutput(output) });
    } catch (error) { if (!controller.signal.aborted && !this.closed) this.send({ type: 'result', id: message.id, error: remoteError(error) }); }
    finally { this.running.delete(message.id); }
  }
  call(method, value, { signal, timeoutMs = this.timeoutMs, validateOutput = x => x } = {}) {
    if (!this.ready) return Promise.reject(new PeerError('unavailable', 'Peer is not ready'));
    if (!this.remoteMethods.includes(method)) return Promise.reject(new PeerError('unsupported', 'Peer does not offer this method'));
    if (signal?.aborted) return Promise.reject(new PeerError('cancelled', 'Call cancelled'));
    const id = crypto.randomUUID();
    return new Promise((resolve, reject) => {
      const finish = (code, message) => { if (!this.pending.has(id)) return; this.pending.delete(id); clearTimeout(timer); signal?.removeEventListener('abort', abort); try { this.send({ type: 'cancel', id }); } catch {} reject(new PeerError(code, message)); };
      const abort = () => finish('cancelled', 'Call cancelled');
      const timer = setTimeout(() => finish('timeout', 'Call timed out'), timeoutMs);
      this.pending.set(id, { resolve, reject, validate: validateOutput, timer, signal, abort });
      signal?.addEventListener('abort', abort, { once: true });
      try { this.send({ type: 'call', id, method, value }); } catch (error) { this.pending.delete(id); clearTimeout(timer); signal?.removeEventListener('abort', abort); reject(error); }
    });
  }
  event(value) { this.send({ type: 'event', value }); }
  state(value) { this.send({ type: 'state', value }); }
  close() {
    if (this.closed) return;
    this.closed = true; this.ready = false;
    this.port.onMessage.removeListener(this.messageListener);
    this.port.onDisconnect.removeListener(this.disconnectListener);
    for (const [id, pending] of this.pending) { clearTimeout(pending.timer); pending.signal?.removeEventListener('abort', pending.abort); pending.reject(new PeerError('closed', 'Peer disconnected')); this.pending.delete(id); }
    for (const controller of this.running.values()) controller.abort(); this.running.clear();
    try { this.port.disconnect(); } catch {}
    this.onDisconnect?.(this);
  }
}

function browserRuntime(runtime) {
  const selected = runtime ?? globalThis.browser?.runtime ?? globalThis.chrome?.runtime;
  if (!selected?.connect || !selected?.onConnectExternal) throw new PeerError('unavailable', 'Browser runtime messaging unavailable');
  return selected;
}

export function createHost({ runtime, methods = {}, admit, authorize, onEvent, onState, onChange, timeoutMs = 10000 } = {}) {
  if (typeof admit !== 'function') throw new PeerError('invalid', 'Host requires an admit callback');
  const names = descriptor(methods); const peers = new Map(); let listener;
  const api = {
    start() {
      if (listener) return;
      const rt = browserRuntime(runtime);
      listener = port => {
        if (port.name !== PORT_NAME || !port.sender?.id) { port.disconnect(); return; }
        const id = port.sender.id;
        const sender = Object.freeze({ id, origin: port.sender.origin, url: port.sender.url, tabId: port.sender.tab?.id, frameId: port.sender.frameId });
        Promise.resolve().then(() => admit(sender)).then(allowed => {
          if (!allowed) { port.disconnect(); return; }
          if (!listener) { port.disconnect(); return; }
          peers.get(id)?.close();
          const connection = new Connection(port, methods, (_c, name) => authorize?.(sender, name),
            (_c, event) => onEvent?.(sender, event), (_c, kind, state) => { if (kind === 'connected') onChange?.(api.peers()); else onState?.(sender, state); },
            () => { if (peers.get(id) === connection) { peers.delete(id); onChange?.(api.peers()); } }, timeoutMs);
          peers.set(id, connection); connection.send({ type: 'hello', methods: names });
        }).catch(() => port.disconnect());
      };
      rt.onConnectExternal.addListener(listener);
    },
    stop() { if (listener) { browserRuntime(runtime).onConnectExternal.removeListener(listener); listener = undefined; } for (const peer of [...peers.values()]) peer.close(); },
    peers() { return [...peers].filter(([, c]) => c.ready).map(([id, c]) => ({ id, methods: [...c.remoteMethods] })); },
    call(id, method, value, options) { const peer = peers.get(id); return peer ? peer.call(method, value, options) : Promise.reject(new PeerError('unavailable', 'Peer not connected')); },
    event(id, value) { const peer = peers.get(id); if (!peer) throw new PeerError('unavailable', 'Peer not connected'); peer.event(value); },
  };
  return api;
}

export function createGuest({ runtime, hostId, methods = {}, authorize, onEvent, onState, onChange, timeoutMs = 10000, reconnectMs = 1000 } = {}) {
  if (!hostId || typeof hostId !== 'string') throw new PeerError('invalid', 'Guest requires hostId');
  const names = descriptor(methods); let connection; let timer; let active = false;
  const api = {
    start() { if (active) return; active = true; connect(); },
    stop() { active = false; clearTimeout(timer); connection?.close(); connection = undefined; },
    connected() { return !!connection?.ready; },
    call(method, value, options) { return connection ? connection.call(method, value, options) : Promise.reject(new PeerError('unavailable', 'Host not connected')); },
    event(value) { if (!connection?.ready) throw new PeerError('unavailable', 'Host not connected'); connection.event(value); },
    state(value) { if (!connection?.ready) throw new PeerError('unavailable', 'Host not connected'); connection.state(value); },
  };
  function connect() {
    if (!active) return;
    try {
      const port = browserRuntime(runtime).connect(hostId, { name: PORT_NAME });
      connection = new Connection(port, methods, (_c, name) => authorize?.(name),
        (_c, event) => onEvent?.(event), (_c, kind, state) => { if (kind === 'connected') onChange?.(true); else onState?.(state); },
        closed => { if (connection === closed) { connection = undefined; onChange?.(false); if (active) timer = setTimeout(connect, reconnectMs); } }, timeoutMs);
      connection.send({ type: 'hello', methods: names });
    } catch { if (active) timer = setTimeout(connect, reconnectMs); }
  }
  return api;
}
