import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const browser = process.env.CHROMIUM_BINARY;
if (!browser) throw new Error('Set CHROMIUM_BINARY to Chromium or Chrome for Testing (branded Chrome 137+ ignores --load-extension).');
const profile = await mkdtemp(join(tmpdir(), 'ext-peer-chromium-'));
const extensions = ['host', 'guest'].map(name => resolve(root, 'dist', name)).join(',');
const child = spawn(browser, [
  '--headless=new', '--no-first-run', '--no-default-browser-check',
  `--user-data-dir=${profile}`, '--remote-debugging-port=0',
  `--disable-extensions-except=${extensions}`, `--load-extension=${extensions}`,
  'about:blank',
], { stdio: 'ignore' });

const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(read, label, timeout = 15000) {
  const end = Date.now() + timeout;
  let last;
  while (Date.now() < end) {
    try { const value = await read(); if (value) return value; } catch (error) { last = error; }
    await delay(150);
  }
  throw new Error(`Timed out waiting for ${label}${last ? `: ${last.message}` : ''}`);
}

class CDP {
  constructor(url) {
    this.socket = new WebSocket(url);
    this.next = 0;
    this.pending = new Map();
    this.listeners = new Map();
    this.socket.addEventListener('message', event => {
      const message = JSON.parse(event.data);
      if (message.method) {
        for (const listener of this.listeners.get(message.method) ?? []) listener(message.params);
        return;
      }
      const pending = this.pending.get(message.id);
      if (!pending) return;
      this.pending.delete(message.id);
      message.error ? pending.reject(new Error(message.error.message)) : pending.resolve(message.result);
    });
  }
  on(method, listener) {
    const listeners = this.listeners.get(method) ?? new Set();
    listeners.add(listener); this.listeners.set(method, listeners);
    return () => listeners.delete(listener);
  }
  async open() {
    if (this.socket.readyState === WebSocket.OPEN) return;
    await new Promise((resolve, reject) => {
      this.socket.addEventListener('open', resolve, { once: true });
      this.socket.addEventListener('error', reject, { once: true });
    });
  }
  async send(method, params = {}) {
    await this.open();
    const id = ++this.next;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      this.socket.send(JSON.stringify({ id, method, params }));
    });
  }
  async eval(expression) {
    const result = await this.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description ?? result.exceptionDetails.text);
    return result.result.value;
  }
  close() { this.socket.close(); }
}

let port;
async function targets() {
  const response = await fetch(`http://127.0.0.1:${port}/json/list`);
  if (!response.ok) throw new Error(`DevTools returned ${response.status}`);
  return response.json();
}
async function extensionTarget(id) {
  return until(async () => (await targets()).find(target => target.type === 'service_worker' && target.url === `chrome-extension://${id}/background.mjs`), `worker ${id}`);
}
async function extensionIDs() {
  return until(async () => {
    const workers = (await targets()).filter(target => target.type === 'service_worker' && target.url.endsWith('/background.mjs'));
    const ids = {};
    for (const worker of workers) {
      const cdp = new CDP(worker.webSocketDebuggerUrl);
      try {
        const name = await cdp.eval('chrome.runtime.getManifest().name');
        if (name === 'Ext Peer Host Example') ids.host = new URL(worker.url).host;
        if (name === 'Ext Peer Guest Example') ids.guest = new URL(worker.url).host;
      } finally { cdp.close(); }
    }
    return ids.host && ids.guest ? ids : null;
  }, 'both example extensions');
}
async function popup(id) {
  const version = await (await fetch(`http://127.0.0.1:${port}/json/version`)).json();
  const browserCDP = new CDP(version.webSocketDebuggerUrl);
  try {
    const { targetId } = await browserCDP.send('Target.createTarget', { url: `chrome-extension://${id}/popup.html` });
    const target = await until(async () => (await targets()).find(item => item.id === targetId && item.webSocketDebuggerUrl), `popup ${id}`);
    const cdp = new CDP(target.webSocketDebuggerUrl);
    cdp.targetId = targetId;
    await until(async () => (await cdp.eval('document.readyState')) === 'complete', `popup load ${id}`);
    return cdp;
  } finally { browserCDP.close(); }
}
async function stopWorker(id) {
  const page = (await targets()).find(target => target.type === 'page' && target.url === 'about:blank');
  if (!page) throw new Error('Missing DevTools control page');
  const cdp = new CDP(page.webSocketDebuggerUrl);
  const versions = new Map();
  cdp.on('ServiceWorker.workerVersionUpdated', event => {
    for (const worker of event.versions) versions.set(worker.versionId, worker);
  });
  try {
    await cdp.send('ServiceWorker.enable');
    const worker = await until(() => [...versions.values()].find(item => item.scriptURL === `chrome-extension://${id}/background.mjs` && item.runningStatus === 'running'), `running worker ${id}`);
    await cdp.send('ServiceWorker.stopWorker', { versionId: worker.versionId });
  } finally { cdp.close(); }
}
const run = async (name, check) => { await check(); console.log(`PASS ${name}`); };

try {
  port = Number((await until(async () => {
    const contents = await readFile(join(profile, 'DevToolsActivePort'), 'utf8');
    return contents.split('\n')[0];
  }, 'DevTools port')).trim());
  const ids = await extensionIDs();
  console.log(`Chromium example IDs: host=${ids.host} guest=${ids.guest}`);
  let host = await popup(ids.host);
  let guest = await popup(ids.guest);
  await until(async () => Array.isArray(await host.eval('chrome.runtime.sendMessage({type:"peers"})')), 'host listener');
  await until(async () => typeof (await guest.eval('chrome.runtime.sendMessage({type:"status"})'))?.connected === 'boolean', 'guest listener');
  await host.eval(`chrome.storage.local.set({guestId:${JSON.stringify(ids.guest)}})`);
  assert.deepEqual(await guest.eval(`chrome.runtime.sendMessage({type:'configure',hostId:${JSON.stringify(ids.host)}})`), { ok: true });
  await run('admitted guest registers', async () => {
    await until(async () => (await host.eval('chrome.runtime.sendMessage({type:"peers"})'))?.some(peer => peer.id === ids.guest), 'guest registration');
  });
  await run('host receives guest state and event', async () => {
    await until(async () => {
      const value = await host.eval('chrome.storage.local.get(["guestState","lastEvent"])');
      return value.guestState?.some(spoke => spoke.id === ids.guest && spoke.state?.status === 'ready') && value.lastEvent?.kind === 'guest.ready';
    }, 'guest state and event');
  });
  await run('guest calls host', async () => {
    assert.deepEqual(await guest.eval('chrome.runtime.sendMessage({type:"call",method:"host.greet",value:"guest"})'), { value: 'Hello, guest' });
  });
  await run('host calls guest', async () => {
    assert.deepEqual(await host.eval(`chrome.runtime.sendMessage({type:'echo',id:${JSON.stringify(ids.guest)},value:'hello'})`), { value: 'hello' });
  });
  await run('host denies restricted method', async () => {
    assert.deepEqual(await guest.eval('chrome.runtime.sendMessage({type:"call",method:"host.secret",value:"guest"})'), { error: 'denied' });
  });
  await run('guest reconnects after host worker restart', async () => {
    const before = (await extensionTarget(ids.host)).id;
    await stopWorker(ids.host);
    await until(async () => !(await targets()).some(target => target.id === before), 'old host worker exit');
    await until(async () => (await host.eval('chrome.runtime.sendMessage({type:"peers"})'))?.some(peer => peer.id === ids.guest), 'guest reconnection');
    assert.notEqual((await extensionTarget(ids.host)).id, before);
    assert.deepEqual(await guest.eval('chrome.runtime.sendMessage({type:"call",method:"host.greet",value:"again"})'), { value: 'Hello, again' });
  });
  await run('guest reconnects after guest worker restart', async () => {
    const before = (await extensionTarget(ids.guest)).id;
    await stopWorker(ids.guest);
    await until(async () => !(await targets()).some(target => target.id === before), 'old guest worker exit');
    await until(async () => (await guest.eval('chrome.runtime.sendMessage({type:"status"})'))?.connected, 'guest worker reconnect');
    assert.notEqual((await extensionTarget(ids.guest)).id, before);
    await until(async () => (await host.eval('chrome.runtime.sendMessage({type:"peers"})'))?.some(peer => peer.id === ids.guest), 'guest re-registration');
    assert.deepEqual(await host.eval(`chrome.runtime.sendMessage({type:'echo',id:${JSON.stringify(ids.guest)},value:'again'})`), { value: 'again' });
  });
  host.close(); guest.close();
} finally {
  child.kill();
  await delay(500);
  await rm(profile, { recursive: true, force: true });
}
