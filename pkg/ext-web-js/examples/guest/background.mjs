import { createSpoke, PeerError } from './peer.mjs';
const text = value => { if (typeof value !== 'string' || value.length > 256) throw new PeerError('invalid', 'Expected short text'); return value; };
let guest;
async function start() {
  guest?.stop();
  const { hostId } = await chrome.storage.local.get('hostId');
  if (!hostId) return;
  guest = createSpoke({ hostId, name: 'Echo Guest', initialState: { status: 'ready' }, reconnectMs: 1000, methods: {
    'guest.echo': { validateInput: text, validateOutput: text, handle: text },
    'guest.delay': { validateInput: text, validateOutput: text, handle: (value, { signal }) => new Promise((resolve, reject) => { const timer = setTimeout(() => resolve(value), 3000); signal.addEventListener('abort', () => { clearTimeout(timer); reject(new PeerError('cancelled', 'Cancelled')); }, { once: true }); }) },
  }, onChange: connected => { if (connected) guest.event({ kind: 'guest.ready' }); } });
  guest.start();
}
void start();
chrome.runtime.onMessage.addListener((message, _sender, respond) => {
  if (message?.type === 'configure') { chrome.storage.local.set({ hostId: message.hostId }).then(start).then(() => respond({ ok: true })); return true; }
  if (message?.type === 'status') { respond({ connected: guest?.connected() ?? false }); return; }
  if (message?.type === 'call') { guest.call(message.method, message.value, { validateOutput: text }).then(value => respond({ value }), error => respond({ error: error.code })); return true; }
});
