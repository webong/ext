// Portable CTX v1 host/guest SDK. No process discovery, ambient permissions,
// global registry, dependency loader, or implicit retry is installed.
export const API_VERSION = 'ext.plugin/v1';
export const MAX_FRAME_BYTES = 24 << 20;
/** The most text one progress event may carry, in bytes of UTF-8. */
export const MAX_PROGRESS_TEXT = 2048;
/** A retry hint is whole milliseconds, at most one year. */
export const MAX_RETRY_AFTER_MS = 365 * 24 * 60 * 60 * 1000;
const encoder = new TextEncoder();
const decoder = new TextDecoder('utf-8', { fatal: true });
const identifier = /^[a-z0-9][a-z0-9._/-]{0,255}$/;
const revision = /^[A-Za-z0-9][A-Za-z0-9._:+/-]{0,255}$/;
const copy = value => structuredClone(value);
const invalid = () => {
    throw new Error('invalid plugin contract');
};
function object(value, fields) {
    if (!value || typeof value !== 'object' || Array.isArray(value) || Object.keys(value).some(k => !fields.includes(k)))
        invalid();
}
function matches(pattern, value) {
    return typeof value === 'string' && pattern.test(value);
}
// A member Go decodes into a string or a flag leaves alone when it is null.
function optionalString(value) {
    if (value === undefined || value === null)
        return '';
    if (typeof value !== 'string')
        invalid();
    return value;
}
function flag(value) {
    if (value === undefined || value === null)
        return false;
    if (typeof value !== 'boolean')
        invalid();
    return value;
}
function identity(id) {
    object(id, ['id', 'revision', 'version']);
    const version = optionalString(id.version);
    if (!matches(identifier, id.id) || !matches(revision, id.revision) || (version !== '' && !matches(revision, version)))
        invalid();
}
function contract(ref) {
    if (!matches(identifier, ref.name) || !matches(revision, ref.version))
        invalid();
}
const sameIdentity = (a, b) => a.id === b.id && a.revision === b.revision && (a.version ?? '') === (b.version ?? '');
// The contracts a descriptor lists, forward or reverse (the host operations the
// plugin may call). A reverse operation cannot declare progress.
function validateContracts(list, reverse) {
    const refs = new Set();
    for (const c of list) {
        object(c, ['name', 'version', 'operations']);
        contract(c);
        const ref = JSON.stringify([c.name, c.version]);
        if (refs.has(ref) || !Array.isArray(c.operations) || !c.operations.length || c.operations.length > 256)
            invalid();
        refs.add(ref);
        const names = new Set();
        for (const op of c.operations) {
            object(op, ['name', 'surface', 'progress']);
            const surface = optionalString(op.surface);
            if (!matches(identifier, op.name) || op.name === 'plugin.hello' || names.has(op.name) || (surface !== '' && !matches(identifier, surface)))
                invalid();
            if (flag(op.progress) && reverse)
                invalid();
            names.add(op.name);
        }
    }
}
export function validateDescriptor(d) {
    object(d, ['apiVersion', 'identity', 'contracts', 'reverse']);
    if (d.apiVersion !== API_VERSION)
        invalid();
    identity(d.identity);
    if (!Array.isArray(d.contracts) || !d.contracts.length || d.contracts.length > 64)
        invalid();
    validateContracts(d.contracts, false);
    if (d.reverse !== undefined && d.reverse !== null) {
        if (!Array.isArray(d.reverse) || d.reverse.length > 64)
            invalid();
        validateContracts(d.reverse, true);
    }
}
function find(contracts, ref, operation) {
    const op = (contracts ?? []).find(c => c.name === ref.name && c.version === ref.version)?.operations.find(o => o.name === operation);
    if (!op)
        throw new Error('unsupported plugin operation');
    return copy(op);
}
export function lookup(d, ref, operation) {
    return find(d.contracts, ref, operation);
}
/** An operation the plugin declared it may call on its host. */
export function lookupReverse(d, ref, operation) {
    return find(d.reverse, ref, operation);
}
const sameOperation = (a, b) => (a.surface ?? '') === (b.surface ?? '') && flag(a.progress) === flag(b.progress);
function matchContracts(expected, actual) {
    expected = expected ?? [];
    actual = actual ?? [];
    if (expected.length !== actual.length)
        throw new Error('plugin handshake mismatch');
    for (const c of expected) {
        const other = actual.find(o => c.name === o.name && c.version === o.version);
        if (!other || c.operations.length !== other.operations.length)
            throw new Error('plugin handshake mismatch');
        for (const op of c.operations) {
            if (!sameOperation(find(actual, c, op.name), op))
                throw new Error('plugin handshake mismatch');
        }
    }
}
export function matchHandshake(expected, actual) {
    validateDescriptor(expected);
    validateDescriptor(actual);
    if (!sameIdentity(expected.identity, actual.identity))
        throw new Error('plugin handshake mismatch');
    matchContracts(expected.contracts, actual.contracts);
    matchContracts(expected.reverse, actual.reverse);
}
// Check duplicate keys before JSON.parse (which otherwise silently overwrites
// them). The parser also enforces the shared nesting and frame limits.
export function decodeJSON(bytes) {
    if (typeof bytes === 'string')
        bytes = encoder.encode(bytes);
    if (bytes.byteLength > MAX_FRAME_BYTES)
        invalid();
    const text = decoder.decode(bytes);
    let i = 0;
    const ws = () => {
        while (/[\x20\t\r\n]/.test(text[i] ?? 'x'))
            i++;
    };
    const str = () => {
        const start = i;
        if (text[i++] !== '"')
            invalid();
        let ended = false;
        while (i < text.length) {
            const c = text[i++];
            if (c === '\\') {
                i++;
                continue;
            }
            if (c === '"') {
                ended = true;
                break;
            }
        }
        if (!ended)
            invalid();
        // An unpaired surrogate escape is not text, as in Go.
        const s = JSON.parse(text.slice(start, i));
        if (!s.isWellFormed())
            invalid();
        return s;
    };
    const value = depth => {
        ws();
        if (depth > 64)
            invalid();
        const c = text[i];
        if (c === '{' || c === '[') {
            i++;
            ws();
            const end = c === '{' ? '}' : ']';
            const keys = new Set();
            if (text[i] === end) {
                i++;
                return;
            }
            while (true) {
                if (c === '{') {
                    ws();
                    const key = str();
                    if (keys.has(key))
                        invalid();
                    keys.add(key);
                    ws();
                    if (text[i++] !== ':')
                        invalid();
                }
                value(depth + 1);
                ws();
                if (text[i] === end) {
                    i++;
                    break;
                }
                if (text[i++] !== ',')
                    invalid();
            }
            return;
        }
        if (c === '"') {
            str();
            return;
        }
        const start = i;
        while (i < text.length && !/[\x20\t\r\n,}\]]/.test(text[i]))
            i++;
        if (i === start)
            invalid();
        const scalar = JSON.parse(text.slice(start, i));
        if (typeof scalar === "number" && (!Number.isFinite(scalar) || Number.isInteger(scalar) && !Number.isSafeInteger(scalar)))
            invalid();
    };
    value(0);
    ws();
    if (i !== text.length)
        invalid();
    return JSON.parse(text);
}
function encode(value) {
    const bytes = encoder.encode(JSON.stringify(value));
    decodeJSON(bytes);
    return bytes;
}
const rfc3339 = /^\d{4}-\d\d-\d\d[Tt]\d\d:\d\d:\d\d(\.\d+)?([Zz]|[+-]\d\d:\d\d)$/;
// The instant of 0001-01-01T00:00:00Z, which Go's zero time is.
const zeroInstant = Date.parse('0001-01-01T00:00:00Z');
function instant(value) {
    if (typeof value !== 'string' || !rfc3339.test(value))
        invalid();
    const t = Date.parse(value);
    if (!Number.isFinite(t) || t === zeroInstant)
        invalid();
    return t;
}
export function validateRequest(d, r) {
    object(r, ['apiVersion', 'id', 'plugin', 'contract', 'operation', 'surface', 'deadline', 'payload', 'progress', 'parent']);
    object(r.contract, ['name', 'version']);
    identity(r.plugin);
    if (r.apiVersion !== API_VERSION || !matches(revision, r.id))
        invalid();
    instant(r.deadline);
    optionalString(r.parent);
    // The rate a host sets for a call whose operation declared progress.
    if (r.progress !== undefined && r.progress !== null) {
        object(r.progress, ['eventsPerSecond', 'bytesPerSecond']);
        for (const key of ['eventsPerSecond', 'bytesPerSecond']) {
            const v = r.progress[key];
            if (v !== undefined && v !== null && !Number.isSafeInteger(v))
                invalid();
        }
    }
    if (!sameIdentity(d.identity, r.plugin) || (lookup(d, r.contract, r.operation).surface ?? '') !== (optionalString(r.surface)))
        invalid();
    if ('payload' in r)
        encode(r.payload);
}
function validateResponse(r, id) {
    object(r, ['apiVersion', 'id', 'payload', 'error']);
    if (r.apiVersion !== API_VERSION || r.id !== id)
        invalid();
    const error = r.error != null;
    if (error === ('payload' in r))
        invalid();
    if (error) {
        object(r.error, ['code', 'message', 'retryAfterMilliseconds']);
        // A retry hint is whole milliseconds, at most one year.
        if (!matches(identifier, r.error.code) || typeof r.error.message !== 'string' || encoder.encode(r.error.message).length > 4096 || !Number.isSafeInteger(r.error.retryAfterMilliseconds ?? 0) || (r.error.retryAfterMilliseconds ?? 0) < 0 || (r.error.retryAfterMilliseconds ?? 0) > MAX_RETRY_AFTER_MS)
            invalid();
    }
    else
        encode(r.payload);
}
function bounded(signal, milliseconds) {
    if (!Number.isFinite(milliseconds) || milliseconds <= 0)
        throw new Error('deadline exceeded');
    return AbortSignal.any([...(signal ? [signal] : []), AbortSignal.timeout(Math.max(1, Math.min(2147483647, Math.ceil(milliseconds))))]);
}
function check(signal) {
    signal?.throwIfAborted();
}
async function abortable(promise, signal) {
    check(signal);
    let listener;
    const canceled = new Promise((_, reject) => {
        listener = () => reject(signal.reason);
        signal.addEventListener('abort', listener, { once: true });
    });
    try {
        return await Promise.race([promise, canceled]);
    }
    finally {
        signal.removeEventListener('abort', listener);
    }
}
export class RemoteError extends Error {
    constructor(code, message, retryAfterMilliseconds = 0) {
        super(message);
        this.name = 'RemoteError';
        this.code = code;
        this.retryAfterMilliseconds = retryAfterMilliseconds;
    }
}
// ---- progress events and reverse calls ----
//
// A guest on a transport that carries frames can report progress while a call runs
// and call back into its host (docs/plugin-guest-frames.md). The rules are the Go
// contract's, pinned by lib/plugin/testdata/v1/{frame-cases,progress-limiter-cases}.json.
/** A frame operation failed locally. `code` is `unsupported` (no reporter or no
 * caller on this call), `invalid` (the event breaks the rules; nothing was sent),
 * `closed` (the call is over or the connection ended), `deadline`, `canceled`, or
 * `protocol` (the host broke the framing rules). */
export class FrameError extends Error {
    constructor(code, message = code) {
        super(message);
        this.name = 'FrameError';
        this.code = code;
    }
}
const protocol = () => {
    throw new FrameError('protocol', 'plugin protocol violation');
};
function strictObject(value, fields) {
    if (!value || typeof value !== 'object' || Array.isArray(value) || Object.keys(value).some(k => !fields.includes(k)))
        protocol();
    return value;
}
// A member Go decodes into a string or a uint64: absent and null are empty or zero.
function textMember(m, key) {
    const v = m[key];
    if (v === undefined || v === null)
        return '';
    if (typeof v !== 'string')
        protocol();
    return v;
}
function countMember(m, key) {
    const v = m[key];
    if (v === undefined || v === null)
        return 0;
    if (!Number.isSafeInteger(v) || v < 0)
        protocol();
    return v;
}
const utf8Length = text => encoder.encode(text).length;
function validateEvent(e) {
    if ((e.kind !== 'progress' && e.kind !== 'log') || (e.stream !== '' && e.stream !== 'out' && e.stream !== 'err') || (e.stream !== '' && e.kind !== 'log') || utf8Length(e.text) > MAX_PROGRESS_TEXT || (e.total !== 0 && e.completed > e.total))
        protocol();
}
/** Judges a progress frame: its parent (the call it belongs to) and the event. */
export function validateProgressFrame(v) {
    strictObject(v, ['frame', 'apiVersion', 'parent', 'kind', 'stream', 'text', 'completed', 'total', 'dropped']);
    const parent = textMember(v, 'parent');
    const event = { kind: textMember(v, 'kind'), stream: textMember(v, 'stream'), text: textMember(v, 'text'), completed: countMember(v, 'completed'), total: countMember(v, 'total'), dropped: countMember(v, 'dropped') };
    if (textMember(v, 'frame') !== 'progress' || textMember(v, 'apiVersion') !== API_VERSION || !matches(revision, parent))
        protocol();
    validateEvent(event);
    return { parent, event };
}
/** Judges a reverse request frame, as the host reads it. */
export function validateReverseRequest(v) {
    strictObject(v, ['frame', 'apiVersion', 'id', 'parent', 'contract', 'operation', 'deadline', 'payload']);
    const ref = strictObject(v.contract, ['name', 'version']);
    const parent = textMember(v, 'parent');
    if (textMember(v, 'frame') !== 'reverse_request' || textMember(v, 'apiVersion') !== API_VERSION || !matches(revision, textMember(v, 'id')) || (parent !== '' && !matches(revision, parent)) || !matches(identifier, textMember(ref, 'name')) || !matches(revision, textMember(ref, 'version')) || !matches(identifier, textMember(v, 'operation')))
        protocol();
    try {
        instant(v.deadline);
    }
    catch {
        protocol();
    }
}
/** Judges a reverse response, which a guest accepts only for the request `id` it is
 * waiting on. Returns `{payload}` or `{error}`. */
export function validateReverseResponse(v, id) {
    strictObject(v, ['frame', 'apiVersion', 'id', 'payload', 'error']);
    if (textMember(v, 'frame') !== 'reverse_response' || textMember(v, 'apiVersion') !== API_VERSION || textMember(v, 'id') !== id)
        protocol();
    const failed = v.error !== undefined && v.error !== null;
    // A response carries a payload (null counts) or an error, never both or neither.
    if (failed === Object.hasOwn(v, 'payload'))
        protocol();
    if (!failed)
        return { payload: v.payload };
    const e = strictObject(v.error, ['code', 'message', 'retryAfterMilliseconds']);
    const retry = e.retryAfterMilliseconds ?? 0;
    if (!matches(identifier, textMember(e, 'code')) || utf8Length(textMember(e, 'message')) > 4096 || !Number.isSafeInteger(retry) || retry < 0 || retry > MAX_RETRY_AFTER_MS)
        protocol();
    return { error: { code: e.code, message: textMember(e, 'message'), retryAfterMilliseconds: retry } };
}
/** Cuts text into pieces that each fit one event: at most MAX_PROGRESS_TEXT bytes of
 * UTF-8, never ending inside a character. A lone surrogate becomes U+FFFD, because
 * an event carrying one is a protocol violation. */
export function splitProgressText(text) {
    const pieces = [];
    let piece = '';
    let bytes = 0;
    for (const char of text.toWellFormed()) {
        const size = utf8Length(char);
        if (bytes + size > MAX_PROGRESS_TEXT) {
            pieces.push(piece);
            piece = '';
            bytes = 0;
        }
        piece += char;
        bytes += size;
    }
    if (piece !== '')
        pieces.push(piece);
    return pieces;
}
/** The sender's rate limiter: a token bucket counted in events and bytes of text with
 * a burst of twice the rate, and a count of events dropped since the last one that
 * passed. Time is a monotonic clock in nanoseconds, given by the caller. */
export class Limiter {
    #events;
    #bytes;
    #eventRate;
    #byteRate;
    #last;
    #dropped = 0;
    constructor({ eventsPerSecond = 0, bytesPerSecond = 0 } = {}, nowNs = 0) {
        this.#eventRate = eventsPerSecond > 0 ? eventsPerSecond : DEFAULT_PROGRESS_EVENTS;
        this.#byteRate = bytesPerSecond > 0 ? bytesPerSecond : DEFAULT_PROGRESS_BYTES;
        this.#events = 2 * this.#eventRate;
        this.#bytes = 2 * this.#byteRate;
        this.#last = nowNs;
    }
    /** The number of events dropped since the previous one that passed (which the
     * caller attaches to this event), or null when the rate limit drops it. */
    admit(textBytes, nowNs) {
        const delta = nowNs - this.#last;
        // Seconds as Go's Duration.Seconds computes them.
        const elapsed = Math.trunc(delta / 1e9) + (delta % 1e9) / 1e9;
        if (elapsed > 0) {
            this.#events = Math.min(this.#events + elapsed * this.#eventRate, 2 * this.#eventRate);
            this.#bytes = Math.min(this.#bytes + elapsed * this.#byteRate, 2 * this.#byteRate);
            this.#last = nowNs;
        }
        if (this.#events < 1 || this.#bytes < textBytes) {
            this.#dropped++;
            return null;
        }
        this.#events--;
        this.#bytes -= textBytes;
        const dropped = this.#dropped;
        this.#dropped = 0;
        return dropped;
    }
    /** The drops not yet reported, reset to zero. */
    pending() {
        const dropped = this.#dropped;
        this.#dropped = 0;
        return dropped;
    }
}
const DEFAULT_PROGRESS_EVENTS = 50;
const DEFAULT_PROGRESS_BYTES = 65536;
const nowNs = () => Math.round(performance.now() * 1e6);
const refused = code => new RemoteError(code, 'reverse call refused');
// Every frame the connection writes goes through one queue, so frames never
// interleave and each is written after the ones asked for before it.
class Link {
    #frames;
    #tail = Promise.resolve();
    #issued = 0;
    #waiting = new Map();
    #queue = [];
    #next = null;
    #busy = false;
    #ended = new AbortController();
    failure = null;
    constructor(frames) {
        this.#frames = frames;
    }
    get closed() {
        return this.#ended.signal;
    }
    write(value) {
        const written = this.#tail.then(() => this.#frames.write(value));
        this.#tail = written.catch(() => { });
        return written;
    }
    /** Reads and routes frames until the connection ends. Never rejects. */
    async run(signal) {
        try {
            while (true) {
                const frame = await this.#frames.read(signal);
                if (frame === null)
                    return this.end(null);
                this.#route(frame);
            }
        }
        catch (e) {
            this.end(e);
        }
    }
    #route(frame) {
        if (!frame || typeof frame !== 'object' || Array.isArray(frame))
            protocol();
        if (!('frame' in frame)) {
            // A request while a call is in flight is a violation.
            if (this.#busy)
                protocol();
            this.#busy = true;
            if (this.#next) {
                const resolve = this.#next;
                this.#next = null;
                resolve(frame);
            }
            else
                this.#queue.push(frame);
            return;
        }
        if (frame.frame !== 'reverse_response')
            protocol();
        const id = typeof frame.id === 'string' ? frame.id : '';
        const waiter = this.#waiting.get(id);
        if (waiter) {
            // Judged against the request it answers.
            let answer;
            try {
                answer = validateReverseResponse(frame, id);
            }
            catch {
                protocol();
            }
            this.#waiting.delete(id);
            waiter.resolve(answer);
            return;
        }
        // An answer to a request that was abandoned is dropped; one to an id never
        // issued is a violation.
        const n = /^g([0-9]+)$/.exec(id);
        if (!n || Number(n[1]) < 1 || Number(n[1]) > this.#issued)
            protocol();
    }
    /** The next request, or null when the host closed the connection. */
    next() {
        if (this.#queue.length)
            return Promise.resolve(this.#queue.shift());
        if (this.#ended.signal.aborted)
            return Promise.resolve(null);
        return new Promise(resolve => {
            this.#next = resolve;
        });
    }
    /** The call is answered: the next request may come. */
    idle() {
        this.#busy = false;
    }
    end(failure) {
        if (this.#ended.signal.aborted)
            return;
        this.failure = failure ?? null;
        this.#ended.abort();
        if (this.#next) {
            this.#next(null);
            this.#next = null;
        }
        for (const waiter of this.#waiting.values())
            waiter.reject(new FrameError('closed', 'plugin connection ended'));
        this.#waiting.clear();
    }
    /** Calls a host operation and waits for the answer. */
    async reverse(descriptor, parent, ref, operation, payload, signal, deadline) {
        try {
            lookupReverse(descriptor, ref, operation);
        }
        catch {
            throw refused('reverse_undeclared');
        }
        const id = `g${++this.#issued}`;
        const frame = { frame: 'reverse_request', apiVersion: API_VERSION, id, ...(parent ? { parent } : {}), contract: { name: ref.name, version: ref.version }, operation, deadline: new Date(deadline).toISOString(), ...(payload === undefined ? {} : { payload }) };
        try {
            validateReverseRequest(frame);
            encode(frame);
        }
        catch {
            throw new FrameError('invalid', 'reverse request breaks the rules');
        }
        const answered = new Promise((resolve, reject) => this.#waiting.set(id, { resolve, reject }));
        answered.catch(() => { });
        const abandon = () => this.#waiting.delete(id);
        let timer;
        const expired = new Promise((_, reject) => {
            timer = setTimeout(() => reject(new FrameError('deadline', 'reverse call deadline exceeded')), Math.max(0, Math.min(2147483647, deadline - Date.now())));
        });
        expired.catch(() => { });
        let aborted;
        const cancel = signal ? new Promise((_, reject) => {
            aborted = () => reject(new FrameError('canceled', 'reverse call canceled'));
            if (signal.aborted)
                aborted();
            else
                signal.addEventListener('abort', aborted, { once: true });
        }) : new Promise(() => { });
        cancel.catch(() => { });
        try {
            try {
                await this.write(frame);
            }
            catch {
                throw new FrameError('closed', 'plugin connection ended');
            }
            const answer = await Promise.race([answered, expired, cancel]);
            if (answer.error)
                throw new RemoteError(answer.error.code, answer.error.message, answer.error.retryAfterMilliseconds);
            return answer.payload;
        }
        catch (e) {
            abandon();
            throw e;
        }
        finally {
            clearTimeout(timer);
            if (aborted)
                signal.removeEventListener('abort', aborted);
        }
    }
}
// Sends the progress events of one call.
class Reporter {
    #link;
    #parent;
    #limiter;
    #finished = false;
    constructor(link, parent, limits) {
        this.#link = link;
        this.#parent = parent;
        this.#limiter = new Limiter(limits, nowNs());
    }
    async report(event) {
        const e = { kind: event?.kind ?? '', stream: event?.stream ?? '', text: event?.text ?? '', completed: event?.completed ?? 0, total: event?.total ?? 0, dropped: 0 };
        try {
            if (typeof e.kind !== 'string' || typeof e.stream !== 'string' || typeof e.text !== 'string' || !Number.isSafeInteger(e.completed) || e.completed < 0 || !Number.isSafeInteger(e.total) || e.total < 0)
                protocol();
            validateEvent(e);
        }
        catch {
            throw new FrameError('invalid', 'progress event breaks the rules');
        }
        const dropped = this.#limiter.admit(utf8Length(e.text), nowNs());
        if (dropped === null)
            return 'dropped';
        e.dropped = dropped;
        if (this.#finished)
            throw new FrameError('closed', 'the call is over');
        try {
            await this.#link.write(progressFrame(this.#parent, e));
        }
        catch {
            throw new FrameError('closed', 'plugin connection ended');
        }
        return 'sent';
    }
    /** Ends reporting after a marker for events never reported: nothing may follow
     * the response. */
    async finish() {
        if (this.#finished)
            return;
        this.#finished = true;
        const pending = this.#limiter.pending();
        if (pending > 0) {
            try {
                await this.#link.write(progressFrame(this.#parent, { kind: 'log', stream: '', text: '', completed: 0, total: 0, dropped: pending }));
            }
            catch { }
        }
    }
}
function progressFrame(parent, e) {
    const frame = { frame: 'progress', apiVersion: API_VERSION, parent, kind: e.kind };
    if (e.stream !== '')
        frame.stream = e.stream;
    if (e.text !== '')
        frame.text = e.text;
    for (const key of ['completed', 'total', 'dropped'])
        if (e[key] !== 0)
            frame[key] = e[key];
    return frame;
}
const unsupported = () => Promise.reject(new FrameError('unsupported', 'no frame channel on this call'));
/** What a call can do beyond returning a result. On a transport with no frames
 * (an in-process call, the C ABI) there is no reporter and no caller. */
const NO_FRAMES = Object.freeze({ progress: unsupported, progressLimits: null, reverse: null, sessionReverse: null, finish: async () => { } });
function callFrames(link, descriptor, request) {
    let declared = false;
    try {
        declared = flag(lookup(descriptor, request.contract, request.operation).progress);
    }
    catch { }
    const raw = request.progress;
    const limits = declared && raw ? { eventsPerSecond: raw.eventsPerSecond > 0 ? raw.eventsPerSecond : DEFAULT_PROGRESS_EVENTS, bytesPerSecond: raw.bytesPerSecond > 0 ? raw.bytesPerSecond : DEFAULT_PROGRESS_BYTES } : null;
    const reporter = limits ? new Reporter(link, request.id, limits) : null;
    const caller = parent => ({
        call: (ref, operation, payload, signal) => link.reverse(descriptor, parent, ref, operation, payload, signal, parent === request.id ? Date.parse(request.deadline) : Date.now() + 30000),
        concurrent: true
    });
    return {
        progress: reporter ? event => reporter.report(event) : unsupported,
        progressLimits: limits ? { ...limits } : null,
        reverse: caller(request.id),
        sessionReverse: caller(''),
        finish: () => reporter ? reporter.finish() : Promise.resolve()
    };
}
export class Guest {
    #descriptor;
    #handler;
    #timeout;
    constructor(descriptor, { handler, timeoutMs = 30000 }) {
        validateDescriptor(descriptor);
        if (typeof handler !== 'function' || !Number.isFinite(timeoutMs) || timeoutMs <= 0)
            invalid();
        this.#descriptor = copy(descriptor);
        this.#handler = handler;
        this.#timeout = timeoutMs;
    }
    async handshake(signal) {
        check(signal);
        return copy(this.#descriptor);
    }
    async invoke(request, signal, frames) {
 request=copy(request);
        let r = {
            apiVersion: API_VERSION, id: request.id
        };
        try {
            validateRequest(this.#descriptor, request);
        }
        catch {
            return {
                ...r, error: {
                    code: 'invalid_request', message: 'request does not match selected contract'
                }
            };
        }
        try {
            const bound = bounded(signal, Math.min(this.#timeout, Date.parse(request.deadline) - Date.now()));
            check(bound);
            const payload = await this.#handler(copy(request), bound, frames ?? NO_FRAMES);
            check(bound);
            r.payload = payload === undefined ? null : copy(payload);
        }
        catch (e) {
            r.error = e instanceof RemoteError ? {
                code: e.code, message: e.message, retryAfterMilliseconds: e.retryAfterMilliseconds
            } : {
                code: 'operation_failed', message: 'plugin operation failed'
            };
        }
        validateResponse(r, request.id);
        return r;
    }
}
export class Session {
    #descriptor;
    #backend;
    #authorize;
    #timeout;
    #state = 'ready';
    #next = 0;
    #active = new Set();
    #close;
    static async open(descriptor, options) {
 descriptor=copy(descriptor); options={...options};
        validateDescriptor(descriptor);
        if (typeof options.verify !== 'function' || typeof options.authorize !== 'function' || typeof options.connect !== 'function')
            throw new Error('plugin admission denied');
        const signal = bounded(options.signal, options.timeoutMs ?? 30000);
        await options.verify(copy(descriptor), signal);
        check(signal);
        let backend;
        try {
            backend = await options.connect(copy(descriptor), signal);
            check(signal);
            const actual = await abortable(backend.handshake(signal), signal);
            matchHandshake(descriptor, actual);
            return new Session(descriptor, backend, options);
        }
        catch (e) {
            await backend?.close();
            throw e;
        }
    }
    constructor(descriptor, backend, options) {
        this.#descriptor = copy(descriptor);
        this.#backend = backend;
        this.#authorize = options.authorize;
        this.#timeout = options.timeoutMs ?? 30000;
    }
    get state() {
        return this.#state;
    }
    get descriptor() {
        return copy(this.#descriptor);
    }
    async call(ref, operation, payload, signal) {
        if (this.#state !== 'ready')
            throw new Error('plugin session closed or draining');
        const bound = bounded(signal, this.#timeout);
        check(bound);
        const op = lookup(this.#descriptor, ref, operation);
        const id = String(++this.#next);
        const request = {
            apiVersion: API_VERSION, id, plugin: copy(this.#descriptor.identity), contract: {
                name: ref.name, version: ref.version
            }, operation, deadline: new Date(Date.now() + this.#timeout).toISOString(), ...(op.surface ? { surface: op.surface } : {}), ...(payload === undefined ? {} : { payload: copy(payload) })
        };
        validateRequest(this.#descriptor, request);
        let release;
        const done = new Promise(resolve => {
            release = resolve;
        });
        this.#active.add(done);
        try {
            await this.#authorize(copy(request), bound);
            check(bound);
 if (this.#state === "closed" || this.#state === "failed") throw new Error("plugin session closed");
            let response;
            try {
                response = await abortable(this.#backend.invoke(request, bound), bound);
                check(bound);
                validateResponse(response, id);
            }
            catch (e) {
                if (this.#state !== 'closed') this.#state = 'failed';
                await this.#closeBackend();
                throw e;
            }
            if (response.error)
                throw new RemoteError(response.error.code, response.error.message, response.error.retryAfterMilliseconds);
            return copy(response.payload);
        }
        finally {
            this.#active.delete(done);
            release();
        }
    }
    #closeBackend() {
        this.#close ??= Promise.resolve().then(() => this.#backend.close());
        return this.#close;
    }
    async close(signal) {
        if (this.#state === 'closed' || this.#state === 'failed')
            return this.#closeBackend();
        if (this.#state === 'draining')
            throw new Error('plugin session draining');
        const bound = bounded(signal, this.#timeout);
        check(bound);
        this.#state = 'draining';
        try {
            await abortable(Promise.all([...this.#active]), bound);
        }
        catch (e) {
            if (this.#state === 'draining')
                this.#state = 'ready';
            throw e;
        }
        if (this.#state === 'draining')
            this.#state = 'closed';
        return this.#closeBackend();
    }
    async abort() {
        this.#state = 'closed';
        return this.#closeBackend();
    }
}
// Codec methods are explicit runtime validators. TypeScript types alone do not
// validate untrusted JSON. Use the same domain schema/version on both sides.
export class Registry {
    #identity;
    #methods = new Map();
    #reverse = [];
    constructor(id) {
        identity(id);
        this.#identity = copy(id);
    }
    register(method, handler) {
        if (typeof handler !== 'function' || typeof method.input?.parse !== 'function' || typeof method.output?.parse !== 'function')
            invalid();
        const key = JSON.stringify([method.contract.name, method.contract.version, method.operation.name]);
        if (this.#methods.has(key))
            invalid();
        const entry = {
            ...method, contract: copy(method.contract), operation: copy(method.operation), handler
        };
        this.#methods.set(key, entry);
        try {
            validateDescriptor(this.descriptor);
        }
        catch (e) {
            this.#methods.delete(key);
            throw e;
        }
        return this;
    }
    /** Declares host operations the plugin may call (`call.reverse`). */
    declareReverse(ref, operations) {
        this.#reverse.push({ ...copy(ref), operations: copy(operations) });
        try {
            validateDescriptor(this.descriptor);
        }
        catch (e) {
            this.#reverse.pop();
            throw e;
        }
        return this;
    }
    get descriptor() {
        const contracts = [];
        for (const m of this.#methods.values()) {
            let c = contracts.find(c => c.name === m.contract.name && c.version === m.contract.version);
            if (!c) {
                c = {
                    ...copy(m.contract), operations: []
                };
                contracts.push(c);
            }
            c.operations.push(copy(m.operation));
        }
        return {
            apiVersion: API_VERSION, identity: copy(this.#identity), contracts, ...(this.#reverse.length ? { reverse: copy(this.#reverse) } : {})
        };
    }
    guest({ authorize, timeoutMs } = {}) {
        if (typeof authorize !== 'function')
            throw new Error('plugin admission denied');
        const methods = new Map(this.#methods);
        return new Guest(this.descriptor, {
            timeoutMs, handler: async (request, signal, frames) => {
                try {
                    await authorize(copy(request), signal);
                }
                catch {
                    throw new RemoteError('denied', 'operation denied');
                }
                const m = methods.get(JSON.stringify([request.contract.name, request.contract.version, request.operation]));
                if (!m)
                    invalid();
                let input;
                try {
                    input = m.input.parse(request.payload ?? null);
                }
                catch {
                    throw new RemoteError('invalid_input', 'request payload does not satisfy operation contract');
                }
                const result = await m.handler(input, signal, request, frames);
                return m.output.parse(result);
            }
        });
    }
}
export async function call(caller, method, input, signal) {
    input = method.input.parse(input);
    const result = await caller.call(method.contract, method.operation.name, input, signal);
    return method.output.parse(result);
}
class Frames {
    constructor(io) {
        this.io = io;
        this.buffer = new Uint8Array(4096);
        this.start = this.end = this.scan = 0;
    }
    async read(signal) {
        while (true) {
            const offset = this.buffer.subarray(this.scan, this.end).indexOf(10);
            if (offset >= 0) {
                const newline = this.scan + offset;
                if (newline - this.start > MAX_FRAME_BYTES) invalid();
                const frame = this.buffer.subarray(this.start, newline);
                this.start = this.scan = newline + 1;
                return decodeJSON(frame);
            }
            this.scan = this.end;
            if (this.end - this.start > MAX_FRAME_BYTES) invalid();
            const next = await this.io.read(signal);
            if (next === null) {
                if (this.end !== this.start) throw new Error('truncated plugin frame');
                return null;
            }
            if (!(next instanceof Uint8Array) || next.length === 0 || next.length > MAX_FRAME_BYTES + 1) invalid();
            // Compact and grow geometrically: fragmented frames remain linear
            // in total bytes, with at most two frame bounds retained in memory.
            if (this.end + next.length > this.buffer.length || this.start === this.end) {
                this.buffer.copyWithin(0, this.start, this.end);
                this.end -= this.start;
                this.scan -= this.start;
                this.start = 0;
            }
            if (this.end + next.length > this.buffer.length) {
                const capacity = Math.min(2 * MAX_FRAME_BYTES + 1, Math.max(this.buffer.length * 2, this.end + next.length));
                const buffer = new Uint8Array(capacity);
                buffer.set(this.buffer.subarray(0, this.end));
                this.buffer = buffer;
            }
            this.buffer.set(next, this.end);
            this.end += next.length;
        }
    }
    async write(value, signal) {
        const data = encode(value);
        const frame = new Uint8Array(data.length + 1);
        frame.set(data);
        frame[data.length] = 10;
        await this.io.write(frame, signal);
    }
}
export class JSONLineBackend {
    #frames;
    #tail = Promise.resolve();
    #closed = false;
    constructor(io) {
        this.#frames = new Frames(io);
    }
    async #exchange(request, signal) {
        signal = bounded(signal, Math.max(1, Date.parse(request.deadline) - Date.now()));
        let release;
        const previous = this.#tail;
        this.#tail = new Promise(resolve => {
            release = resolve;
        });
        try {
            await abortable(previous, signal);
        }
        catch (e) {
            previous.finally(release);
            throw e;
        }
        try {
            check(signal);
            if (this.#closed)
                throw new Error('plugin connection closed');
            await abortable(this.#frames.write(request, signal), signal);
            const r = await abortable(this.#frames.read(signal), signal);
            if (r === null)
                throw new Error('plugin connection ended');
            validateResponse(r, request.id);
            return r;
        }
        catch (e) {
            await this.close();
            throw e;
        }
        finally {
            release();
        }
    }
    async handshake(signal) {
        const r = await this.#exchange({
            apiVersion: API_VERSION, id: 'hello', plugin: {
                id: '', revision: ''
            }, contract: {
                name: '', version: ''
            }, operation: 'plugin.hello', deadline: new Date(Date.now() + 30000).toISOString()
        }, signal);
        if (r.error)
            throw new RemoteError(r.error.code, r.error.message);
        validateDescriptor(r.payload);
        return r.payload;
    }
    invoke(request, signal) {
        return this.#exchange(request, signal);
    }
    async close() {
        if (!this.#closed) {
            this.#closed = true;
            await this.#frames.io.close();
        }
    }
}
export async function serveGuest(io, guest, signal) {
    const link = new Link(new Frames(io));
    let hello = false;
    let descriptor;
    const stop = () => {
        void io.close();
    };
    signal?.addEventListener('abort', stop, { once: true });
    // The reader runs for as long as the connection lasts, so a handler waiting for
    // the host's answer to a reverse call never keeps it from being read.
    void link.run(signal);
    try {
        check(signal);
        while (true) {
            const request = await link.next();
            if (request === null)
                break;
            let response;
            if (!hello) {
                validateHello(request);
                descriptor = await guest.handshake(bounded(signal, Date.parse(request.deadline) - Date.now()));
                validateDescriptor(descriptor);
                response = {
                    apiVersion: API_VERSION, id: 'hello', payload: descriptor
                };
                hello = true;
            }
            else {
                const frames = callFrames(link, descriptor, request);
                try {
                    response = await guest.invoke(request, AbortSignal.any([...(signal ? [signal] : []), link.closed]), frames);
                }
                finally {
                    await frames.finish(); // a marker for events never reported, before the response
                }
            }
            validateResponse(response, request.id);
            // A violation found while the handler ran ends the session unanswered.
            if (link.failure)
                break;
            link.idle(); // before the response, so the next request is never early
            await link.write(response);
        }
        if (link.failure)
            throw link.failure;
    }
    finally {
        signal?.removeEventListener('abort', stop);
        link.end(null);
        await io.close();
    }
}
// The host's hello names no plugin, contract or surface of its own: the Go host
// sends none, and the C engine's launcher too. Each may be absent or empty.
function validateHello(request) {
    object(request, ['apiVersion', 'id', 'plugin', 'contract', 'operation', 'surface', 'deadline']);
    for (const [member, fields] of [['plugin', ['id', 'revision', 'version']], ['contract', ['name', 'version']]]) {
        if (request[member] === undefined)
            continue;
        object(request[member], fields);
        if (Object.values(request[member]).some(v => v !== ''))
            invalid();
    }
    if (request.apiVersion !== API_VERSION || request.id !== 'hello' || request.operation !== 'plugin.hello' || (request.surface !== undefined && request.surface !== '') || !Number.isFinite(Date.parse(request.deadline)) || Date.parse(request.deadline) <= Date.now())
        invalid();
}
// Compile the portable ext.schema/v1 vocabulary into a runtime codec.
export function schemaCodec(source) {
    const s = copy(source);
    let budget = 1024;
    function validate(s, depth = 0) {
        object(s, ['type', 'properties', 'required', 'additionalProperties', 'items', 'enum', 'minimum', 'maximum', 'minLength', 'maxLength', 'minItems', 'maxItems']);
        if (--budget < 0 || depth > 16)
            invalid();
        if (!['object', 'array', 'string', 'number', 'integer', 'boolean', 'null'].includes(s.type))
            invalid();
        for (const k of ['minLength', 'maxLength', 'minItems', 'maxItems'])
            if (s[k] !== undefined && (!Number.isSafeInteger(s[k]) || s[k] < 0))
                invalid();
        for (const k of ['minimum', 'maximum'])
            if (s[k] !== undefined && (typeof s[k] !== 'number' || !Number.isFinite(s[k])))
                invalid();
        if (s.minimum !== undefined && s.maximum !== undefined && s.minimum > s.maximum)
            invalid();
        if (s.maxLength > 0 && s.minLength > s.maxLength || s.maxItems > 0 && s.minItems > s.maxItems)
            invalid();
        if (s.type !== 'object' && (s.properties !== undefined || s.required !== undefined || s.additionalProperties !== undefined))
            invalid();
        if (s.type !== 'array' && (s.items !== undefined || s.minItems !== undefined || s.maxItems !== undefined))
            invalid();
        if (s.type !== 'string' && (s.enum !== undefined || s.minLength !== undefined || s.maxLength !== undefined))
            invalid();
        if (!['number', 'integer'].includes(s.type) && (s.minimum !== undefined || s.maximum !== undefined))
            invalid();
        if (s.type === 'object') {
            const props = s.properties ?? {};
            if (!props || typeof props !== 'object' || Array.isArray(props) || Object.keys(props).length > 256)
                invalid();
            for (const [k, v] of Object.entries(props)) {
                if (!k || encoder.encode(k).length > 256)
                    invalid();
                validate(v, depth + 1);
            }
            if (s.additionalProperties !== undefined && typeof s.additionalProperties !== 'boolean')
                invalid();
            if (s.required !== undefined && (!Array.isArray(s.required) || new Set(s.required).size !== s.required.length || s.required.some(k => !Object.hasOwn(props, k))))
                invalid();
        }
        if (s.type === 'array') {
            if (!s.items)
                invalid();
            validate(s.items, depth + 1);
        }
        if (s.enum !== undefined && (!Array.isArray(s.enum) || s.enum.length > 256 || new Set(s.enum).size !== s.enum.length || s.enum.some(v => typeof v !== 'string' || encoder.encode(v).length > 4096)))
            invalid();
    }
    validate(s);
    function check(s, v) {
        switch (s.type) {
            case 'object':
                if (!v || typeof v !== 'object' || Array.isArray(v))
                    invalid();
                for (const k of s.required ?? [])
                    if (!Object.hasOwn(v, k))
                        invalid();
                for (const [k, value] of Object.entries(v)) {
                    if (Object.hasOwn(s.properties ?? {}, k))
                        check(s.properties[k], value);
                    else if (!s.additionalProperties)
                        invalid();
                }
                break;
            case 'array':
                if (!Array.isArray(v) || v.length < (s.minItems ?? 0) || s.maxItems > 0 && v.length > s.maxItems)
                    invalid();
                for (const x of v)
                    check(s.items, x);
                break;
            case 'string':
                if (typeof v !== 'string' || [...v].length < (s.minLength ?? 0) || s.maxLength > 0 && [...v].length > s.maxLength || s.enum?.length && !s.enum.includes(v))
                    invalid();
                break;
            case 'number':
            case 'integer':
                if (typeof v !== 'number' || !Number.isFinite(v) || s.type === 'integer' && !Number.isSafeInteger(v) || s.minimum !== undefined && v < s.minimum || s.maximum !== undefined && v > s.maximum)
                    invalid();
                break;
            case 'boolean':
                if (typeof v !== 'boolean')
                    invalid();
                break;
            case 'null':
                if (v !== null)
                    invalid();
                break;
        }
    }
    return { parse(value) {
            encode(value);
            check(s, value);
            return copy(value);
        } };
}
export const healthMethod = Object.freeze({
    contract: Object.freeze({
        name: 'ext.health', version: 'v1'
    }), operation: Object.freeze({
        name: 'check', surface: 'observation'
    }), input: schemaCodec({ type: 'object' }), output: schemaCodec({
        type: 'object', properties: {
            status: {
                type: 'string', enum: ['healthy', 'degraded', 'unhealthy']
            }, ready: { type: 'boolean' }
        }, required: ['status', 'ready']
    })
});
// ext.configuration update: the same wire shape and limits as the Go library.
// `values` is any JSON document; the guest's own schema then checks it. It must
// not carry credentials.
const configurationInput = {
    parse(value) {
        object(value, ['instance', 'revision', 'values']);
        for (const key of ['instance', 'revision']) {
            if (typeof value[key] !== 'string' || !value[key] || value[key].length > 256)
                invalid();
        }
        if (!('values' in value))
            invalid();
        encode(value.values);
        return copy(value);
    }
};
export const configurationMethod = Object.freeze({
    contract: Object.freeze({
        name: 'ext.configuration', version: 'v1'
    }), operation: Object.freeze({
        name: 'update', surface: 'configuration'
    }), input: configurationInput, output: schemaCodec({
        type: 'object', properties: {
            instance: { type: 'string' }, revision: { type: 'string' }
        }, required: ['instance', 'revision']
    })
});
// Pull streams work over the existing unary protocol; each read is authorized
// independently by the supplied Caller. No read or action is retried.
export async function openStream(caller, parameters, signal) {
    const ref = {
        name: 'ext.stream', version: 'v1'
    };
    const opened = await caller.call(ref, 'open', { parameters }, signal);
    object(opened, ['id']);
    if (typeof opened.id !== 'string' || !opened.id || opened.id.length > 64)
        invalid();
    let sequence = 0, done = false;
    return {
        async read(limit = 64, signal) {
            if (done)
                return {
                    items: [], done: true
                };
            if (!Number.isInteger(limit) || limit < 1 || limit > 256)
                invalid();
            try {
                const b = await caller.call(ref, 'read', {
                    id: opened.id, sequence: ++sequence, limit
                }, signal);
                object(b, ['items', 'done']);
                if (!Array.isArray(b.items) || b.items.length > limit || typeof b.done !== 'boolean' || encoder.encode(JSON.stringify(b.items)).length > (1 << 20) + 512)
                    invalid();
                done = b.done;
                return b;
            }
            catch (e) {
                done = true;
                throw e;
            }
        },
        async close(signal) {
            done = true;
            await caller.call(ref, 'close', { id: opened.id }, signal);
        }
    };
}
