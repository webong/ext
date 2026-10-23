// The frames conformance guest (lib/plugin/testdata/v1/frames-descriptor.json,
// docs/plugin-guest-frames.md): reports progress and calls its host. It serves
// standard input and output; protocol stdout is reserved for JSON-line frames.
import { Registry, serveGuest, splitProgressText, FrameError, RemoteError } from '../../index.mjs';
import { nodeIO } from '../../node.mjs';

const FRAMES = { name: 'ext.frames', version: 'v1' };
const HOST = { name: 'host.svc', version: 'v1' };
const any = { parse: value => value };

// How a reverse call came out, in the shape the suite reads.
async function outcome(pending) {
    try {
        return { result: (await pending) ?? null };
    } catch (e) {
        if (e instanceof RemoteError)
            return { error: { code: e.code, message: e.message, ...(e.retryAfterMilliseconds ? { retry: e.retryAfterMilliseconds } : {}) } };
        const other = e instanceof FrameError && ['closed', 'deadline', 'canceled'].includes(e.code) ? e.code : 'failed';
        return { error: { other } };
    }
}

// Outcomes of session-scoped calls, from background work to "collect".
const background = [];
let collecting = null;
const deposit = value => {
    if (collecting) {
        const resolve = collecting;
        collecting = null;
        resolve(value);
    } else
        background.push(value);
};

const handlers = {
    async stream(_input, _signal, _request, frames) {
        await frames.progress({ kind: 'progress', completed: 1, total: 3 }).catch(() => { });
        await frames.progress({ kind: 'log', stream: 'out', text: 'hello' }).catch(() => { });
        await frames.progress({ kind: 'log', stream: 'err', text: 'oops' }).catch(() => { });
        return { done: true };
    },
    async flood(input, _signal, _request, frames) {
        let dropped = 0;
        for (let i = 0; i < (input?.count ?? 300); i++)
            if (await frames.progress({ kind: 'log', text: 'x' }).catch(() => null) === 'dropped')
                dropped++;
        return { dropped };
    },
    limits(_input, _signal, _request, frames) {
        const l = frames.progressLimits;
        return { declared: l !== null, events: l?.eventsPerSecond ?? 0, bytes: l?.bytesPerSecond ?? 0 };
    },
    async split(input, _signal, _request, frames) {
        if (typeof input?.text !== 'string')
            throw new Error('text is required');
        const pieces = splitProgressText(input.text);
        for (const piece of pieces)
            await frames.progress({ kind: 'log', stream: 'out', text: piece }).catch(() => { });
        return { pieces: pieces.length };
    },
    async plain(_input, _signal, _request, frames) {
        const refused = await frames.progress({ kind: 'log', text: 'never sent' }).then(() => null, e => e);
        return { unsupported: refused instanceof FrameError && refused.code === 'unsupported', limits: frames.progressLimits !== null };
    },
    async call(input, signal, _request, frames) {
        if (!frames.reverse)
            return { error: { other: 'unsupported' } };
        if (typeof input?.op !== 'string')
            throw new Error('op is required');
        return outcome(frames.reverse.call(HOST, input.op, input.payload ?? null, signal));
    },
    async twice(_input, signal, _request, frames) {
        const first = await frames.reverse.call(HOST, 'sign', 1, signal);
        const second = await frames.reverse.call(HOST, 'sign', 2, signal);
        return { first, second };
    },
    async idle(_input, _signal, _request, frames) {
        const session = frames.sessionReverse;
        setTimeout(() => {
            void outcome(session.call(HOST, 'sign', { n: 1 }, AbortSignal.timeout(10000))).then(deposit);
        }, 20);
        return { started: true };
    },
    async collect() {
        if (background.length)
            return background.shift();
        return new Promise(resolve => {
            const timer = setTimeout(() => {
                collecting = null;
                resolve({ timeout: true });
            }, 5000);
            collecting = value => {
                clearTimeout(timer);
                resolve(value);
            };
        });
    },
    async burst(_input, signal, _request, frames) {
        const counts = {};
        await Promise.all(Array.from({ length: 6 }, async () => {
            const key = await frames.reverse.call(HOST, 'hold', null, signal).then(() => 'ok', e => e instanceof RemoteError ? e.code : 'other');
            counts[key] = (counts[key] ?? 0) + 1;
        }));
        return counts;
    }
};

const registry = new Registry({ id: 'ctx/frames', revision: 'fixture-1' });
for (const [name, progress] of [['stream', true], ['flood', true], ['limits', true], ['split', true], ['plain', false], ['call', false], ['twice', false], ['idle', false], ['collect', false], ['burst', false]])
    registry.register({ contract: FRAMES, operation: progress ? { name, progress } : { name }, input: any, output: any }, handlers[name]);
registry.declareReverse(HOST, ['sign', 'secret', 'empty', 'fail', 'errok', 'errretryok', 'big', 'hold'].map(name => name === 'sign' ? { name, surface: 'signing' } : { name }));
// These examples explicitly allow only their declared local fixture API.
const descriptor = registry.descriptor;
const guest = registry.guest({
    authorize(request) {
        if (!descriptor.contracts.some(c => c.name === request.contract.name && c.version === request.contract.version && c.operations.some(o => o.name === request.operation)))
            throw new Error('denied');
    }
});
await serveGuest(nodeIO(process.stdin, process.stdout), guest);
