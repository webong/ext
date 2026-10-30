import { createHost, PeerError } from './peer.mjs';
const text = value => { if (typeof value !== 'string' || value.length > 256) throw new PeerError('invalid', 'Expected short text'); return value; };
const host = createHost({
  admit: async sender => sender.id === (await chrome.storage.local.get('guestId')).guestId,
  authorize: (_sender, method) => { if (method !== 'host.greet') throw new PeerError('denied', 'Method denied'); },
  methods: {
    'host.greet': { validateInput: text, validateOutput: text, handle: name => `Hello, ${name}` },
    'host.secret': { validateInput: text, validateOutput: text, handle: () => 'unreachable' },
  },
  onEvent: async (_sender, value) => chrome.storage.local.set({ lastEvent: value }),
  onState: async (_sender, value) => chrome.storage.local.set({ guestState: value }),
});
host.start();
chrome.runtime.onMessage.addListener((message, _sender, respond) => {
  if (message?.type === 'peers') { respond(host.peers()); return; }
  if (message?.type === 'echo') {
    host.call(message.id, 'guest.echo', message.value, { validateOutput: text }).then(value => respond({ value }), error => respond({ error: error.message }));
    return true;
  }
});
