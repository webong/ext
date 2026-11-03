import { PeerError } from './index.mjs';

const record = value => value && typeof value === 'object' && !Array.isArray(value);
const token = value => typeof value === 'string' && /^[A-Za-z0-9._-]{8,128}$/.test(value);
const CHANNEL = 'ext.web.sandbox/v1';
const bounded = value => {
  let bytes;
  try { bytes = new TextEncoder().encode(JSON.stringify(value)).length; }
  catch { throw new PeerError('invalid', 'Bridge message must be JSON serializable'); }
  if (bytes > 1024 * 1024) throw new PeerError('too_large', 'Bridge message exceeds 1 MiB');
  return value;
};

/** Establish a MessagePort with a caller-created sandbox using a fresh nonce. */
export function connectSandboxHost({ host, guest, nonce, context = {}, createChannel = () => new MessageChannel(), timeoutMs = 10000 }) {
  if (!host?.addEventListener || !host?.removeEventListener || !guest?.postMessage || !token(nonce) || !record(context) || typeof createChannel !== 'function' || !Number.isSafeInteger(timeoutMs) || timeoutMs < 1) throw new PeerError('invalid', 'Invalid sandbox host options');
  return new Promise((resolve, reject) => {
    let port; let done = false;
    const cleanup = () => { clearTimeout(timer); host.removeEventListener('message', receive); };
    const fail = message => { if (done) return; done = true; cleanup(); port?.close(); reject(new PeerError('unavailable', message)); };
    const receive = event => {
      if (done || event.source !== guest || !record(event.data) || event.data.channel !== CHANNEL || event.data.nonce !== nonce) return;
      if (event.data.type === 'ready' && !port) {
        try {
          const pair = createChannel(); port = pair.port1;
          guest.postMessage({ channel: CHANNEL, type: 'connect', nonce, context }, '*', [pair.port2]);
        } catch { fail('Sandbox channel could not be created'); }
      } else if (event.data.type === 'connected' && port) {
        done = true; cleanup(); resolve(port);
      } else if (event.data.type === 'failed') fail('Sandbox guest failed');
    };
    const timer = setTimeout(() => fail('Sandbox connection timed out'), timeoutMs);
    host.addEventListener('message', receive);
  });
}

/** Run inside a sandbox page; the caller owns its creation and policy. */
export function startSandboxGuest({ self, parent, nonce, load = () => {}, connect }) {
  if (!self?.addEventListener || !self?.removeEventListener || !parent?.postMessage || !token(nonce) || typeof connect !== 'function') throw new PeerError('invalid', 'Invalid sandbox guest options');
  let stopped = false; let accepted = false;
  const report = type => { if (!stopped) parent.postMessage({ channel: CHANNEL, type, nonce }, '*'); };
  const receive = event => {
    if (stopped || accepted || event.source !== parent || !record(event.data) || event.data.channel !== CHANNEL || event.data.type !== 'connect' || event.data.nonce !== nonce || event.ports?.length !== 1 || !record(event.data.context)) return;
    accepted = true;
    Promise.resolve().then(() => connect(event.data.context, event.ports[0])).then(() => report('connected'), () => report('failed'));
  };
  self.addEventListener('message', receive);
  Promise.resolve().then(load).then(() => report('ready'), () => report('failed'));
  return () => { stopped = true; self.removeEventListener('message', receive); };
}

/** Page-visible calls; expose only methods safe for any script on the page. */
export function createPageBridgeClient({ target, channel, timeoutMs = 5000 }) {
  if (!target?.addEventListener || !target?.removeEventListener || !target?.postMessage || !token(channel) || !Number.isSafeInteger(timeoutMs) || timeoutMs < 1) throw new PeerError('invalid', 'Invalid page bridge options');
  const pending = new Map(); let closed = false;
  const receive = event => {
    if (event.source !== target || !record(event.data) || event.data.channel !== channel || event.data.type !== 'response') return;
    const waiter = pending.get(event.data.id); if (!waiter) return;
    pending.delete(event.data.id); clearTimeout(waiter.timer);
    try { bounded(event.data); } catch (error) { waiter.reject(error); return; }
    if (event.data.error) waiter.reject(new PeerError('remote_error', String(event.data.error)));
    else waiter.resolve(event.data.value);
  };
  target.addEventListener('message', receive);
  return {
    call(method, value) {
      if (closed) return Promise.reject(new PeerError('closed', 'Page bridge closed'));
      if (typeof method !== 'string' || !/^[a-z][a-z0-9.-]{0,127}$/.test(method)) return Promise.reject(new PeerError('invalid', 'Invalid page method'));
      const id = crypto.randomUUID();
      return new Promise((resolve, reject) => {
        const timer = setTimeout(() => { pending.delete(id); reject(new PeerError('timeout', 'Page call timed out')); }, timeoutMs);
        pending.set(id, { resolve, reject, timer });
        try { target.postMessage(bounded({ channel, type: 'request', id, method, value }), '*'); }
        catch (error) { pending.delete(id); clearTimeout(timer); reject(error); }
      });
    },
    close() { if (closed) return; closed = true; target.removeEventListener('message', receive); for (const waiter of pending.values()) { clearTimeout(waiter.timer); waiter.reject(new PeerError('closed', 'Page bridge closed')); } pending.clear(); },
  };
}

export function createPageBridgeReceiver({ target, channel, allowedMethods, dispatch }) {
  if (!target?.addEventListener || !target?.removeEventListener || !target?.postMessage || !token(channel) || !(allowedMethods instanceof Set) || typeof dispatch !== 'function') throw new PeerError('invalid', 'Invalid page receiver options');
  const receive = event => {
    if (event.source !== target || !record(event.data) || event.data.channel !== channel || event.data.type !== 'request' || typeof event.data.id !== 'string') return;
    const { id, method, value } = event.data;
    Promise.resolve().then(async () => {
      bounded(event.data);
      if (!allowedMethods.has(method)) throw new PeerError('denied', 'Page method denied');
      const result = await dispatch(method, value);
      return bounded({ channel, type: 'response', id, value: result });
    }).then(response => target.postMessage(response, '*'), () => target.postMessage({ channel, type: 'response', id, error: 'Page method failed' }, '*'));
  };
  target.addEventListener('message', receive);
  return () => target.removeEventListener('message', receive);
}
