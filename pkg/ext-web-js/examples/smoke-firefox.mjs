import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const browser = process.env.FIREFOX_BINARY;
if (!browser) throw new Error('Set FIREFOX_BINARY to the Firefox executable.');
const profile = await mkdtemp(join(tmpdir(), 'ext-peer-firefox-'));
const child = spawn(browser, ['--headless', '--no-remote', '--profile', profile, '--remote-debugging-port', '0', 'about:blank'], {
  env: { ...process.env, MOZ_REMOTE_ALLOW_SYSTEM_ACCESS: '1' }, stdio: 'ignore',
});
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(read, label, timeout = 20000) {
  const end = Date.now() + timeout;
  let last;
  while (Date.now() < end) {
    try { const value = await read(); if (value) return value; } catch (error) { last = error; }
    await delay(150);
  }
  throw new Error(`Timed out waiting for ${label}${last ? `: ${last.message}` : ''}`);
}

class BiDi {
  constructor(url) {
    this.socket = new WebSocket(url); this.next = 0; this.pending = new Map();
    this.socket.addEventListener('message', event => {
      const message = JSON.parse(event.data);
      const pending = this.pending.get(message.id);
      if (!pending) return;
      this.pending.delete(message.id);
      message.type === 'error' ? pending.reject(new Error(`${message.error}: ${message.message}`)) : pending.resolve(message.result);
    });
  }
  async send(method, params = {}) {
    if (this.socket.readyState !== WebSocket.OPEN) await new Promise((resolve, reject) => {
      this.socket.addEventListener('open', resolve, { once: true });
      this.socket.addEventListener('error', reject, { once: true });
    });
    const id = ++this.next;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      this.socket.send(JSON.stringify({ id, method, params }));
    });
  }
  async eval(context, expression) {
    const result = await this.send('script.evaluate', { expression: `(async () => JSON.stringify(await (${expression})))()`, target: { context }, awaitPromise: true });
    if (result.type === 'exception') throw new Error(result.exceptionDetails?.text ?? 'Firefox script exception');
    if (result.result.type === 'undefined') return undefined;
    return JSON.parse(result.result.value);
  }
  async page(url) {
    const { context } = await this.send('browsingContext.create', { type: 'tab' });
    await this.send('browsingContext.navigate', { context, url, wait: 'complete' });
    return context;
  }
  close() { this.socket.close(); }
}

const run = async (name, check) => { await check(); console.log(`PASS ${name}`); };
let bidi;
try {
  const port = await until(async () => JSON.parse(await readFile(join(profile, 'WebDriverBiDiServer.json'), 'utf8')).ws_port, 'Firefox BiDi port');
  bidi = new BiDi(`ws://127.0.0.1:${port}/session`);
  const session = await bidi.send('session.new', { capabilities: { alwaysMatch: { webSocketUrl: true } } });
  console.log(`Firefox ${session.capabilities.browserVersion}`);
  for (const name of ['host', 'guest']) {
    await bidi.send('webExtension.install', { extensionData: { type: 'path', path: resolve(root, 'dist', `firefox-${name}`) } });
  }
  const installed = (await bidi.send('webExtension.moz:listExtensions')).extensions;
  const base = name => installed.find(item => item.id === `ext-peer-${name}@example.org`)?.policy?.extensionURL;
  if (!base('host') || !base('guest')) throw new Error('Firefox did not expose the temporary example extension URLs');
  let host = await bidi.page(`${base('host')}popup.html`);
  let guest = await bidi.page(`${base('guest')}popup.html`);
  await bidi.eval(host, 'browser.storage.local.set({guestId:"ext-peer-guest@example.org"})');
  assert.deepEqual(await bidi.eval(guest, 'browser.runtime.sendMessage({type:"configure",hostId:"ext-peer-host@example.org"})'), { ok: true });
  await run('admitted guest registers', async () => {
    await until(async () => (await bidi.eval(host, 'browser.runtime.sendMessage({type:"peers"})'))?.some(peer => peer.id === 'ext-peer-guest@example.org'), 'guest registration');
  });
  await run('host receives guest state and event', async () => {
    await until(async () => {
      const value = await bidi.eval(host, 'browser.storage.local.get(["guestState","lastEvent"])');
      return value.guestState?.some(spoke => spoke.state?.status === 'ready') && value.lastEvent?.kind === 'guest.ready';
    }, 'guest state and event');
  });
  await run('guest calls host', async () => {
    assert.deepEqual(await bidi.eval(guest, 'browser.runtime.sendMessage({type:"call",method:"host.greet",value:"guest"})'), { value: 'Hello, guest' });
  });
  await run('host calls guest', async () => {
    assert.deepEqual(await bidi.eval(host, 'browser.runtime.sendMessage({type:"echo",id:"ext-peer-guest@example.org",value:"hello"})'), { value: 'hello' });
  });
  await run('host denies restricted method', async () => {
    assert.deepEqual(await bidi.eval(guest, 'browser.runtime.sendMessage({type:"call",method:"host.secret",value:"guest"})'), { error: 'denied' });
  });
  await run('guest reconnects after host reload', async () => {
    await bidi.eval(host, 'browser.runtime.reload()').catch(() => {});
    host = await bidi.page(`${base('host')}popup.html`);
    await until(async () => (await bidi.eval(host, 'browser.runtime.sendMessage({type:"peers"})'))?.some(peer => peer.id === 'ext-peer-guest@example.org'), 'guest reconnection');
    assert.deepEqual(await bidi.eval(guest, 'browser.runtime.sendMessage({type:"call",method:"host.greet",value:"again"})'), { value: 'Hello, again' });
  });
  await run('guest reconnects after guest reload', async () => {
    await bidi.eval(guest, 'browser.runtime.reload()').catch(() => {});
    guest = await bidi.page(`${base('guest')}popup.html`);
    await until(async () => (await bidi.eval(guest, 'browser.runtime.sendMessage({type:"status"})'))?.connected, 'guest reload connection');
    await until(async () => (await bidi.eval(host, 'browser.runtime.sendMessage({type:"peers"})'))?.some(peer => peer.id === 'ext-peer-guest@example.org'), 'guest re-registration');
    assert.deepEqual(await bidi.eval(host, 'browser.runtime.sendMessage({type:"echo",id:"ext-peer-guest@example.org",value:"again"})'), { value: 'again' });
  });
} finally {
  try { await bidi?.send('browser.close'); } catch {}
  bidi?.close(); child.kill(); await delay(500);
  await rm(profile, { recursive: true, force: true });
}
