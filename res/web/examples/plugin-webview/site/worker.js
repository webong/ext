// A minimal WASI Preview 1 host for a plugin guest that speaks JSON lines on
// stdin/stdout. stdin is a ring buffer in a SharedArrayBuffer so fd_read can
// block (Atomics.wait) while the page feeds it requests.
"use strict";
const ERRNO = { SUCCESS: 0, BADF: 8, INVAL: 28, NOSYS: 52 };
class Exit { constructor(code) { this.code = code; } }

onmessage = async (event) => {
  const { module, sab } = event.data;
  const ctrl = new Int32Array(sab, 0, 4); // [0] wake counter, [1] read, [2] write, [3] closed
  const ring = new Uint8Array(sab, 16);
  const size = ring.length;
  const sleeper = new Int32Array(new SharedArrayBuffer(4));
  let memory, decoder = new TextDecoder(), pending = "";
  const view = () => new DataView(memory.buffer);
  const bytes = () => new Uint8Array(memory.buffer);

  function readStdin(iovs, count, outPtr) {
    const v = view();
    let total = 0;
    for (;;) {
      const seen = Atomics.load(ctrl, 0);
      let read = Atomics.load(ctrl, 1), write = Atomics.load(ctrl, 2);
      if (read === write) {
        if (Atomics.load(ctrl, 3)) break;            // closed: end of input
        Atomics.wait(ctrl, 0, seen);                  // block until the page writes
        continue;
      }
      for (let i = 0; i < count && read !== write; i++) {
        const ptr = v.getUint32(iovs + i * 8, true), len = v.getUint32(iovs + i * 8 + 4, true);
        for (let j = 0; j < len && read !== write; j++) {
          bytes()[ptr + j] = ring[read % size];
          read++; total++;
        }
      }
      Atomics.store(ctrl, 1, read);
      break;
    }
    v.setUint32(outPtr, total, true);
    return ERRNO.SUCCESS;
  }

  function write(fd, iovs, count, outPtr) {
    const v = view();
    let total = 0, text = "";
    for (let i = 0; i < count; i++) {
      const ptr = v.getUint32(iovs + i * 8, true), len = v.getUint32(iovs + i * 8 + 4, true);
      text += decoder.decode(bytes().subarray(ptr, ptr + len), { stream: true });
      total += len;
    }
    if (fd === 1) {
      pending += text;
      let nl;
      while ((nl = pending.indexOf("\n")) >= 0) { postMessage({ line: pending.slice(0, nl) }); pending = pending.slice(nl + 1); }
    } else if (fd === 2) {
      postMessage({ stderr: text });
    } else {
      return ERRNO.BADF;
    }
    v.setUint32(outPtr, total, true);
    return ERRNO.SUCCESS;
  }

  const nowNs = (id) => id === 0 ? BigInt(Date.now()) * 1000000n : BigInt(Math.round(performance.now() * 1e6));

  const wasi = {
    args_sizes_get: (argc, size) => { view().setUint32(argc, 1, true); view().setUint32(size, 6, true); return 0; },
    args_get: (argv, buf) => { view().setUint32(argv, buf, true); bytes().set(new TextEncoder().encode("guest\0"), buf); return 0; },
    environ_sizes_get: (count, size) => { view().setUint32(count, 0, true); view().setUint32(size, 0, true); return 0; },
    environ_get: () => 0,
    clock_time_get: (id, _precision, out) => { view().setBigUint64(out, nowNs(id), true); return 0; },
    random_get: (ptr, len) => { for (let o = 0; o < len; o += 65536) crypto.getRandomValues(bytes().subarray(ptr + o, ptr + Math.min(len, o + 65536))); return 0; },
    fd_fdstat_get: (fd, ptr) => {
      if (fd > 2) return ERRNO.BADF;
      const v = view();
      v.setUint8(ptr, 2); v.setUint16(ptr + 2, 0, true);
      v.setBigUint64(ptr + 8, 0xffffffffffffffffn, true); v.setBigUint64(ptr + 16, 0xffffffffffffffffn, true);
      return 0;
    },
    fd_fdstat_set_flags: () => 0,
    fd_close: () => 0,
    fd_prestat_get: () => ERRNO.BADF,
    fd_prestat_dir_name: () => ERRNO.BADF,
    fd_read: (fd, iovs, count, out) => fd === 0 ? readStdin(iovs, count, out) : ERRNO.BADF,
    fd_write: write,
    sched_yield: () => 0,
    poll_oneoff: (inPtr, outPtr, n, countPtr) => {
      const v = view();
      for (let i = 0; i < n; i++) {
        const sub = inPtr + i * 48, type = v.getUint8(sub + 8);
        let slept = false;
        if (type === 0) { // clock: sleep, then report it
          const timeout = Number(v.getBigUint64(sub + 24, true)), absolute = v.getUint16(sub + 40, true) & 1;
          let ms = timeout / 1e6;
          if (absolute) ms -= Number(nowNs(v.getUint32(sub + 16, true))) / 1e6;
          if (ms > 0) Atomics.wait(sleeper, 0, 0, ms);
          slept = true;
        }
        const ev = outPtr;
        v.setBigUint64(ev, v.getBigUint64(sub, true), true);
        v.setUint16(ev + 8, 0, true); v.setUint8(ev + 10, type);
        v.setBigUint64(ev + 16, 0n, true); v.setUint16(ev + 24, 0, true);
        v.setUint32(countPtr, 1, true);
        if (slept || type !== 0) return 0;
      }
      v.setUint32(countPtr, 0, true);
      return 0;
    },
    proc_exit: (code) => { throw new Exit(code); },
  };
  // Anything else the guest imports reports "not implemented" rather than failing to instantiate.
  const imports = new Proxy({ wasi_snapshot_preview1: new Proxy(wasi, { get: (t, n) => t[n] || (() => ERRNO.NOSYS) }) }, {});

  try {
    const { instance } = await WebAssembly.instantiate(module, imports);
    memory = instance.exports.memory;
    postMessage({ ready: true });
    instance.exports._start();
    postMessage({ exit: 0 });
  } catch (error) {
    if (error instanceof Exit) postMessage({ exit: error.code });
    else postMessage({ failure: String(error && error.stack || error) });
  }
};
