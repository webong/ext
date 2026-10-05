// Portable CTX v1 host/guest SDK. No process discovery, ambient permissions,
// global registry, dependency loader, or implicit retry is installed.
export const API_VERSION = 'ctx.plugin/v1';
export const MAX_FRAME_BYTES = 24 << 20;
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
function identity(id) {
    object(id, ['id', 'revision', 'version']);
    if (!matches(identifier, id.id) || !matches(revision, id.revision) || (id.version !== undefined && id.version !== '' && !matches(revision, id.version)))
        invalid();
}
function contract(ref) {
    if (!matches(identifier, ref.name) || !matches(revision, ref.version))
        invalid();
}
const sameIdentity = (a, b) => a.id === b.id && a.revision === b.revision && (a.version ?? '') === (b.version ?? '');
export function validateDescriptor(d) {
    object(d, ['apiVersion', 'identity', 'contracts']);
    if (d.apiVersion !== API_VERSION)
        invalid();
    identity(d.identity);
    if (!Array.isArray(d.contracts) || !d.contracts.length || d.contracts.length > 64)
        invalid();
    const refs = new Set();
    for (const c of d.contracts) {
        object(c, ['name', 'version', 'operations']);
        contract(c);
        const ref = JSON.stringify([c.name, c.version]);
        if (refs.has(ref) || !Array.isArray(c.operations) || !c.operations.length || c.operations.length > 256)
            invalid();
        refs.add(ref);
        const names = new Set();
        for (const op of c.operations) {
            object(op, ['name', 'surface']);
            if (!matches(identifier, op.name) || op.name === 'plugin.hello' || names.has(op.name) || (op.surface !== undefined && op.surface !== '' && !matches(identifier, op.surface)))
                invalid();
            names.add(op.name);
        }
    }
}
export function lookup(d, ref, operation) {
    const op = d.contracts.find(c => c.name === ref.name && c.version === ref.version)?.operations.find(o => o.name === operation);
    if (!op)
        throw new Error('unsupported plugin operation');
    return copy(op);
}
export function matchHandshake(expected, actual) {
    validateDescriptor(expected);
    validateDescriptor(actual);
    if (!sameIdentity(expected.identity, actual.identity) || expected.contracts.length !== actual.contracts.length)
        throw new Error('plugin handshake mismatch');
    for (const c of expected.contracts) {
        const other = actual.contracts.find(o => c.name === o.name && c.version === o.version);
        if (!other || c.operations.length !== other.operations.length)
            throw new Error('plugin handshake mismatch');
        for (const op of c.operations) {
            if ((lookup(actual, c, op.name).surface ?? '') !== (op.surface ?? ''))
                throw new Error('plugin handshake mismatch');
        }
    }
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
        return JSON.parse(text.slice(start, i));
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
function validateRequest(d, r) {
    object(r, ['apiVersion', 'id', 'plugin', 'contract', 'operation', 'surface', 'deadline', 'payload']);
    object(r.contract, ['name', 'version']);
    identity(r.plugin);
    if (r.apiVersion !== API_VERSION || !matches(revision, r.id) || !Number.isFinite(Date.parse(r.deadline)) || r.deadline.startsWith('0001-01-01T00:00:00'))
        invalid();
    if (!sameIdentity(d.identity, r.plugin) || (lookup(d, r.contract, r.operation).surface ?? '') !== (r.surface ?? ''))
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
        if (!matches(identifier, r.error.code) || typeof r.error.message !== 'string' || encoder.encode(r.error.message).length > 4096 || !Number.isSafeInteger(r.error.retryAfterMilliseconds ?? 0) || (r.error.retryAfterMilliseconds ?? 0) < 0)
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
    async invoke(request, signal) {
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
            const payload = await this.#handler(copy(request), bound);
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
            apiVersion: API_VERSION, identity: copy(this.#identity), contracts
        };
    }
    guest({ authorize, timeoutMs } = {}) {
        if (typeof authorize !== 'function')
            throw new Error('plugin admission denied');
        const methods = new Map(this.#methods);
        return new Guest(this.descriptor, {
            timeoutMs, handler: async (request, signal) => {
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
                const result = await m.handler(input, signal, request);
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
    const frames = new Frames(io);
    let hello = false;
    const stop = () => {
        void io.close();
    };
    signal?.addEventListener('abort', stop, { once: true });
    try {
        check(signal);
        while (true) {
            const request = await frames.read(signal);
            if (request === null)
                return;
            let response;
            if (!hello) {
                object(request, ['apiVersion', 'id', 'plugin', 'contract', 'operation', 'deadline']);
                if (request.apiVersion !== API_VERSION || request.id !== 'hello' || request.operation !== 'plugin.hello' || request.plugin?.id !== '' || request.plugin?.revision !== '' || request.contract?.name !== '' || request.contract?.version !== '' || !Number.isFinite(Date.parse(request.deadline)) || Date.parse(request.deadline) <= Date.now())
                    invalid();
                identityHello(request.plugin);
                object(request.contract, ['name', 'version']);
                const d = await guest.handshake(bounded(signal, Date.parse(request.deadline) - Date.now()));
                validateDescriptor(d);
                response = {
                    apiVersion: API_VERSION, id: 'hello', payload: d
                };
                hello = true;
            }
            else {
                response = await guest.invoke(request, signal);
            }
            validateResponse(response, request.id);
            await frames.write(response, signal);
        }
    }
    finally {
        signal?.removeEventListener('abort', stop);
        await io.close();
    }
}
function identityHello(id) {
    object(id, ['id', 'revision', 'version']);
    if (id.version)
        invalid();
}
// Compile the portable ctx.schema/v1 vocabulary into a runtime codec.
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
        name: 'ctx.health', version: 'v1'
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
// Pull streams work over the existing unary protocol; each read is authorized
// independently by the supplied Caller. No read or action is retried.
export async function openStream(caller, parameters, signal) {
    const ref = {
        name: 'ctx.stream', version: 'v1'
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
