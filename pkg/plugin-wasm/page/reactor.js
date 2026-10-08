// The page side of hosting a WebAssembly reactor (docs/plugin-reactor-abi.md) in a
// web engine: a hidden WKWebView, an Android WebView, or a browser. It instantiates
// the module and answers handshake and invoke messages, and talks to the native host
// only through window.ext.host, the transport-neutral page API of res/web/bundle
// (shim.js). This file is the single source: scripts/plugin-mobile-assets.sh embeds
// it in the Swift and Java hosts, and fails its --check when they drift.
//
// The page defines MODULE_BASE64 (the reactor's bytes) before this script runs.
// Messages are JSON strings. Host to page:  {"seq":N,"op":1|2,"frame":"<ext.plugin JSON>"}.
// Page to host: {"seq":N,"status":S,"frame":"<JSON>"} for each reply, and
// {"kind":"ready"} or {"kind":"failed","reason":"..."} once, after instantiation.
// Frames travel as strings so the guest sees the exact bytes the host sent.
(function () {
  "use strict";
  const host = window.ext && window.ext.host;
  const post = (message) => host.send(JSON.stringify(message));
  const MAX_FRAME = 24 * 1024 * 1024;
  let exports = null;
  let session = 0n;
  let responsePointer = 0;
  let lengthPointer = 0;

  const view = () => new DataView(exports.memory.buffer);
  const bytes = () => new Uint8Array(exports.memory.buffer);
  const encoder = new TextEncoder();
  const decoder = new TextDecoder("utf-8", { fatal: true });

  // The WASI import set of docs/plugin-reactor-abi.md, and nothing else.
  const wasi = {
    clock_time_get(id, _precision, out) {
      const nanoseconds = id === 0
        ? BigInt(Date.now()) * 1000000n
        : BigInt(Math.floor(performance.now() * 1e6));
      view().setBigUint64(out, nanoseconds, true);
      return 0;
    },
    random_get(pointer, length) {
      for (let done = 0; done < length; done += 65536) {
        crypto.getRandomValues(new Uint8Array(exports.memory.buffer, pointer + done, Math.min(65536, length - done)));
      }
      return 0;
    },
    environ_sizes_get(count, size) {
      view().setUint32(count, 0, true);
      view().setUint32(size, 0, true);
      return 0;
    },
    environ_get() { return 0; },
    fd_write(fd, iovs, iovsLength, writtenPointer) {
      if (fd !== 1 && fd !== 2) return 8; // EBADF
      let total = 0, text = "";
      for (let i = 0; i < iovsLength; i++) {
        const pointer = view().getUint32(iovs + i * 8, true);
        const length = view().getUint32(iovs + i * 8 + 4, true);
        text += new TextDecoder().decode(bytes().subarray(pointer, pointer + length));
        total += length;
      }
      if (window.ext.log) window.ext.log(text);
      view().setUint32(writtenPointer, total, true);
      return 0;
    },
    poll_oneoff(subscriptions, events, count, eventsPointer) {
      // Clock subscriptions only: sleep by waiting, the single thread has nothing else to run.
      let ready = 0;
      for (let i = 0; i < count; i++) {
        const base = subscriptions + i * 48;
        if (view().getUint8(base + 8) !== 0) return 28; // EINVAL: not a clock
        const timeout = view().getBigUint64(base + 24, true);
        const absolute = (view().getUint16(base + 40, true) & 1) !== 0;
        const clock = view().getUint32(base + 16, true);
        const now = () => clock === 0 ? BigInt(Date.now()) * 1000000n : BigInt(Math.floor(performance.now() * 1e6));
        const until = absolute ? timeout : now() + timeout;
        while (now() < until) { /* wait */ }
        const event = events + ready * 32;
        view().setBigUint64(event, view().getBigUint64(base, true), true);
        view().setUint16(event + 8, 0, true);
        view().setUint8(event + 10, 0);
        ready++;
      }
      view().setUint32(eventsPointer, ready, true);
      return 0;
    },
    proc_exit(code) { throw new Error("guest called proc_exit(" + code + ")"); },
  };

  function allocate(size) {
    const pointer = exports.ext_plugin_alloc(size) >>> 0;
    if (pointer === 0) throw new Error("guest allocation of " + size + " bytes failed");
    return pointer;
  }

  // One ABI call: the request buffer is per call, the response buffer is reused.
  function call(operation, frame) {
    const request = encoder.encode(frame);
    if (request.length === 0 || request.length > MAX_FRAME) return { status: 1, frame: "" };
    const pointer = allocate(request.length);
    try {
      bytes().set(request, pointer);
      const status = exports.ext_plugin_call(session, operation, pointer, request.length, responsePointer, MAX_FRAME, lengthPointer);
      if (status !== 0) return { status, frame: "" };
      const length = view().getUint32(lengthPointer, true);
      return { status: 0, frame: decoder.decode(bytes().slice(responsePointer, responsePointer + length)) };
    } finally {
      exports.ext_plugin_free(pointer, request.length);
    }
  }

  host.onmessage = function (text) {
    let message;
    try { message = JSON.parse(text); } catch (e) { return; }
    let reply;
    try {
      reply = call(message.op, message.frame);
    } catch (error) {
      // A trap or allocation failure ends the instance; report it as a failed call.
      reply = { status: 3, frame: "" };
      window.ext.log && window.ext.log("reactor failed: " + error);
    }
    post({ seq: message.seq, status: reply.status, frame: reply.frame });
  };

  (async function start() {
    try {
      const raw = atob(MODULE_BASE64);
      const module = new Uint8Array(raw.length);
      for (let i = 0; i < raw.length; i++) module[i] = raw.charCodeAt(i);
      const { instance } = await WebAssembly.instantiate(module, { wasi_snapshot_preview1: wasi });
      exports = instance.exports;
      if (exports._start) throw new Error("the module is a command (_start), not a reactor");
      for (const name of ["memory", "ext_plugin_abi_version", "ext_plugin_open", "ext_plugin_call", "ext_plugin_close", "ext_plugin_alloc", "ext_plugin_free"]) {
        if (!exports[name]) throw new Error("the module does not export " + name);
      }
      if (exports._initialize) exports._initialize();
      if (exports.ext_plugin_abi_version() !== 1) throw new Error("unsupported ext.plugin ABI version");
      session = exports.ext_plugin_open();
      if (session === 0n) throw new Error("the guest could not open a session");
      responsePointer = allocate(MAX_FRAME);
      lengthPointer = allocate(4);
      post({ kind: "ready" });
    } catch (error) {
      post({ kind: "failed", reason: String(error) });
    }
  })();
})();
