// Explicit Node stream binding. Diagnostics belong on stderr when using stdio.
// The binding owns these streams and keeps error listeners until they close.
export function nodeIO(readable, writable, { close = () => {
    readable.destroy();
    writable.destroy();
} } = {}) {
    let failure;
    let closing;
    const record = error => { failure ??= error; };
    for (const stream of new Set([readable, writable])) {
        stream.on('error', record);
        stream.once('close', () => stream.off('error', record));
    }
    const iterator = readable[Symbol.asyncIterator]();
    const io = {
        async read(signal) {
            signal?.throwIfAborted();
            if (failure) throw failure;
            const cancel = () => { void io.close(); };
            signal?.addEventListener('abort', cancel, { once: true });
            try {
                const { value, done } = await iterator.next();
                signal?.throwIfAborted();
                if (failure) throw failure;
                return done ? null : new Uint8Array(value);
            } finally {
                signal?.removeEventListener('abort', cancel);
            }
        },
        async write(bytes, signal) {
            signal?.throwIfAborted();
            if (failure) throw failure;
            const cancel = () => { void io.close(); };
            signal?.addEventListener('abort', cancel, { once: true });
            try {
                await new Promise((resolve, reject) => writable.write(bytes, e => e ? reject(e) : resolve()));
                signal?.throwIfAborted();
                if (failure) throw failure;
            } finally {
                signal?.removeEventListener('abort', cancel);
            }
        },
        close() {
            closing ??= Promise.resolve().then(close);
            return closing;
        }
    };
    return io;
}
