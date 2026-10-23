// The router behind `serveGuest`: progress events, reverse calls and the rules a host
// that breaks them meets. The host is the test, on in-memory pipes.
import test from 'node:test';
import assert from 'node:assert/strict';
import { PassThrough } from 'node:stream';
import { Registry, serveGuest, validateProgressFrame, validateReverseRequest, FrameError, RemoteError } from './index.mjs';
import { nodeIO } from './node.mjs';

const contract = { name: 'test.frames', version: 'v1' };
const hostRef = { name: 'host.svc', version: 'v1' };
const any = { parse: value => value };

const handlers = {
    async events(_input, _signal, _request, frames) {
        const first = await frames.progress({ kind: 'log', text: 'a' });
        const second = await frames.progress({ kind: 'progress', completed: 1, total: 2 });
        return { sent: [first === 'sent', second === 'sent'] };
    },
    async flood(_input, _signal, _request, frames) {
        let dropped = 0;
        for (let i = 0; i < 300; i++)
            if (await frames.progress({ kind: 'log', text: 'x' }) === 'dropped')
                dropped++;
        return { dropped };
    },
    async quiet(_input, _signal, _request, frames) {
        return { unsupported: await frames.progress({ kind: 'log', text: 'x' }).catch(e => e instanceof FrameError && e.code) === 'unsupported' };
    },
    async invalid(_input, _signal, _request, frames) {
        const refuse = event => frames.progress(event).then(() => 'sent', e => e instanceof FrameError ? e.code : 'other');
        return { codes: [await refuse({ kind: 'mystery' }), await refuse({ kind: 'progress', completed: 3, total: 2 }), await refuse({ kind: 'log', text: 'x'.repeat(2049) }), await refuse({ kind: 'log', stream: 'both' })] };
    },
    async ask(input, signal, _request, frames) {
        try {
            return { result: await frames.reverse.call(hostRef, input.op ?? 'sign', input.payload, signal) };
        } catch (e) {
            if (e instanceof RemoteError) return { code: e.code };
            if (e instanceof FrameError) return { closed: e.code === 'closed' };
            throw e;
        }
    },
    async twice(_input, signal, _request, frames) {
        return { both: await Promise.all([frames.reverse.call(hostRef, 'sign', 1, signal), frames.reverse.call(hostRef, 'sign', 2, signal)]) };
    },
    async late(_input, _signal, _request, frames) {
        try {
            await frames.reverse.call(hostRef, 'sign', null, AbortSignal.timeout(100));
        } catch (e) {
            return { canceled: e instanceof FrameError && e.code === 'canceled' };
        }
        return { canceled: false };
    },
    async hold(_input, signal) {
        await new Promise(resolve => signal.addEventListener('abort', resolve, { once: true }));
        return null;
    },
    async session(_input, signal, _request, frames) {
        const answer = frames.sessionReverse ? await frames.sessionReverse.call(hostRef, 'sign', null, signal) : null;
        return { session: frames.sessionReverse !== null, concurrent: frames.reverse?.concurrent === true, answer };
    }
};

function guest() {
    const registry = new Registry({ id: 'test/frames', revision: 'r1' });
    for (const [name, progress] of [['events', true], ['flood', true], ['invalid', true], ['quiet', false], ['ask', false], ['twice', false], ['late', false], ['hold', false], ['session', false]])
        registry.register({ contract, operation: progress ? { name, progress } : { name }, input: any, output: any }, handlers[name]);
    registry.declareReverse(hostRef, [{ name: 'sign' }]);
    return registry.guest({ authorize() { } });
}

class Host {
    #frames = [];
    #waiting = null;
    #text = '';
    #to = new PassThrough();
    #from = new PassThrough();
    #next = 0;
    served;
    constructor({ hello = true } = {}) {
        this.#from.on('data', chunk => {
            this.#text += chunk.toString();
            for (let n = this.#text.indexOf('\n'); n >= 0; n = this.#text.indexOf('\n')) {
                this.#frames.push(JSON.parse(this.#text.slice(0, n)));
                this.#text = this.#text.slice(n + 1);
            }
            this.#wake();
        });
        this.served = serveGuest(nodeIO(this.#to, this.#from), guest()).then(() => null, e => e);
        if (hello)
            this.send({ apiVersion: 'ext.plugin/v1', id: 'hello', plugin: { id: '', revision: '' }, contract: { name: '', version: '' }, operation: 'plugin.hello', deadline: new Date(Date.now() + 30000).toISOString() });
    }
    #wake() {
        const waiting = this.#waiting;
        this.#waiting = null;
        waiting?.();
    }
    send(value) {
        this.#to.write(JSON.stringify(value) + '\n');
    }
    /** The next frame the guest wrote, or null when none comes within `wait`. */
    async tryRecv(wait = 5000) {
        const end = Date.now() + wait;
        while (this.#frames.length === 0) {
            const left = end - Date.now();
            if (left <= 0) return null;
            await new Promise(resolve => {
                const timer = setTimeout(resolve, left);
                this.#waiting = () => { clearTimeout(timer); resolve(); };
            });
        }
        return this.#frames.shift();
    }
    async recv() {
        const frame = await this.tryRecv();
        assert.ok(frame, 'the guest wrote nothing');
        return frame;
    }
    request(operation, payload = null, { progress = false, deadlineMs = 30000 } = {}) {
        const id = String(++this.#next);
        this.send({
            apiVersion: 'ext.plugin/v1', id, plugin: { id: 'test/frames', revision: 'r1' }, contract, operation, payload,
            deadline: new Date(Date.now() + deadlineMs).toISOString(), ...(progress ? { progress: { eventsPerSecond: 5 } } : {})
        });
        return id;
    }
    /** Reads frames up to the response to `id`: those before it, and it. */
    async untilResponse(id) {
        const before = [];
        while (true) {
            const frame = await this.recv();
            if (!('frame' in frame) && frame.id === id) return { before, response: frame };
            before.push(frame);
        }
    }
    async finish() {
        this.#to.end();
        return this.served;
    }
}

async function started() {
    const host = new Host();
    assert.equal((await host.recv()).id, 'hello');
    return host;
}

const answer = (id, body = { payload: 1 }) => ({ frame: 'reverse_response', apiVersion: 'ext.plugin/v1', id, ...body });

test('the descriptor declares progress and reverse', async () => {
    const host = new Host();
    const hello = await host.recv();
    const operations = hello.payload.contracts[0].operations;
    assert.ok(operations.some(o => o.name === 'events' && o.progress === true));
    assert.ok(operations.filter(o => o.name === 'quiet').every(o => !('progress' in o)));
    assert.equal(hello.payload.reverse[0].name, 'host.svc');
    assert.equal(await host.finish(), null);
});

test('events come in order before the response', async () => {
    const host = await started();
    const id = host.request('events', null, { progress: true });
    const { before, response } = await host.untilResponse(id);
    assert.equal(before.length, 2, JSON.stringify(before));
    for (const frame of before) {
        assert.equal(validateProgressFrame(frame).parent, id);
    }
    assert.deepEqual([before[0].kind, before[0].text], ['log', 'a']);
    assert.deepEqual([before[1].kind, before[1].completed, before[1].total], ['progress', 1, 2]);
    assert.deepEqual(response.payload.sent, [true, true]);
    assert.equal(await host.tryRecv(100), null, 'a frame after the response');
    assert.equal(await host.finish(), null);
});

test('a flood is limited, counted and closed with a marker', async () => {
    const host = await started();
    const id = host.request('flood', null, { progress: true });
    const { before, response } = await host.untilResponse(id);
    const dropped = response.payload.dropped;
    const delivered = before.filter(e => 'text' in e).length;
    const reported = before.reduce((sum, e) => sum + (e.dropped ?? 0), 0);
    assert.ok(delivered >= 10 && delivered + dropped === 300 && reported === dropped, `${delivered} delivered, ${dropped} dropped, ${reported} reported`);
    // The drops still unreported when the handler returned are in a marker with only a kind and a count.
    const last = before.at(-1);
    assert.ok(!('text' in last) && last.kind === 'log' && last.dropped > 0, JSON.stringify(last));
    assert.equal(await host.finish(), null);
});

test('an operation that did not declare progress cannot report', async () => {
    const host = await started();
    const id = host.request('quiet');
    const { before, response } = await host.untilResponse(id);
    assert.deepEqual(before, []);
    assert.equal(response.payload.unsupported, true);
    assert.equal(await host.finish(), null);
});

test('an event that breaks the rules is refused and never sent', async () => {
    const host = await started();
    const id = host.request('invalid', null, { progress: true });
    const { before, response } = await host.untilResponse(id);
    assert.deepEqual(before, []);
    assert.deepEqual(response.payload.codes, ['invalid', 'invalid', 'invalid', 'invalid']);
    assert.equal(await host.finish(), null);
});

test('a reverse call names the call and returns the answer', async () => {
    const host = await started();
    let id = host.request('ask', { op: 'sign', payload: { n: 1 } });
    let request = await host.recv();
    validateReverseRequest(request);
    assert.equal(request.frame, 'reverse_request');
    assert.deepEqual([request.id, request.parent, request.operation], ['g1', id, 'sign']);
    assert.deepEqual(request.contract, hostRef);
    assert.deepEqual(request.payload, { n: 1 });
    host.send(answer('g1', { payload: 'signed' }));
    assert.equal((await host.untilResponse(id)).response.payload.result, 'signed');
    // The host's error comes back with its code, and ids go on counting.
    id = host.request('ask', { op: 'sign' });
    request = await host.recv();
    assert.equal(request.id, 'g2');
    assert.ok(!('payload' in request));
    host.send(answer('g2', { error: { code: 'denied', message: 'reverse call refused' } }));
    assert.equal((await host.untilResponse(id)).response.payload.code, 'denied');
    // An operation the descriptor does not list is refused without a frame.
    id = host.request('ask', { op: 'nothere' });
    const { before, response } = await host.untilResponse(id);
    assert.deepEqual(before, []);
    assert.equal(response.payload.code, 'reverse_undeclared');
    assert.equal(await host.finish(), null);
});

test('reverse calls of one handler run at once and are matched by id', async () => {
    const host = await started();
    const id = host.request('twice');
    const first = await host.recv();
    const second = await host.recv();
    assert.deepEqual([first.id, second.id], ['g1', 'g2']);
    // Answered in the other order.
    host.send(answer('g2', { payload: 'two' }));
    host.send(answer('g1', { payload: 'one' }));
    assert.deepEqual((await host.untilResponse(id)).response.payload.both, ['one', 'two']);
    assert.equal(await host.finish(), null);
});

test('a session-scoped request carries no parent', async () => {
    const host = await started();
    const id = host.request('session');
    const request = await host.recv();
    assert.ok(!('parent' in request), JSON.stringify(request));
    validateReverseRequest(request);
    host.send(answer(request.id, { payload: 'ok' }));
    assert.deepEqual((await host.untilResponse(id)).response.payload, { session: true, concurrent: true, answer: 'ok' });
    assert.equal(await host.finish(), null);
});

test('an answer to an abandoned request is dropped', async () => {
    const host = await started();
    const id = host.request('late');
    const request = await host.recv();
    assert.equal(request.id, 'g1');
    assert.equal((await host.untilResponse(id)).response.payload.canceled, true);
    host.send(answer('g1'));
    const next = host.request('events', null, { progress: true });
    assert.deepEqual((await host.untilResponse(next)).response.payload.sent, [true, true]);
    assert.equal(await host.finish(), null);
});

test('what the host may not do ends the session', async () => {
    const violations = [
        ['a second request during a call', null],
        ['an unknown frame', { frame: 'mystery' }],
        ['an answer to a request never issued', answer('g99')],
        ['a progress frame from the host', { frame: 'progress', apiVersion: 'ext.plugin/v1', parent: '1', kind: 'log' }],
        ['a malformed answer to the request waiting', answer('g1', { payload: 1, extra: 1 })],
        ['an answer with both payload and error', answer('g1', { payload: 1, error: { code: 'x', message: 'm' } })],
        ['an answer with neither payload nor error', answer('g1', {})],
        ['a frame that is not an object', 7]
    ];
    for (const [what, frame] of violations) {
        const host = await started();
        const id = host.request('ask', { op: 'sign' });
        assert.equal((await host.recv()).id, 'g1');
        if (frame === null) host.request('events', null, { progress: true });
        else host.send(frame);
        const failure = await host.finish();
        assert.ok(failure instanceof FrameError && failure.code === 'protocol', `${what}: ${failure}`);
        // The call that was in flight is never answered.
        for (let f = await host.tryRecv(50); f; f = await host.tryRecv(50))
            assert.ok('frame' in f || f.id !== id, `${what}: answered ${JSON.stringify(f)}`);
    }
});

test('a connection that ends releases a waiting handler', async () => {
    const host = await started();
    const id = host.request('ask', { op: 'sign' });
    assert.equal((await host.recv()).id, 'g1');
    // The host goes away: the handler is released, and a call cut off by the end of the
    // connection is answered as a failed one.
    const finished = host.finish();
    const { response } = await host.untilResponse(id);
    assert.equal(response.error.code, 'operation_failed');
    assert.equal(await finished, null);
});

test('a handler is cancelled when the connection ends', async () => {
    const host = await started();
    const id = host.request('hold');
    await new Promise(resolve => setTimeout(resolve, 50));
    const finished = host.finish();
    assert.equal((await host.untilResponse(id)).response.error.code, 'operation_failed');
    assert.equal(await finished, null);
});

test('a transport without frames gives no reverse caller and no reporter', async () => {
    const g = guest();
    const request = operation => ({
        apiVersion: 'ext.plugin/v1', id: '1', plugin: { id: 'test/frames', revision: 'r1' }, contract, operation, payload: {},
        deadline: new Date(Date.now() + 5000).toISOString(), progress: {}
    });
    assert.equal((await g.invoke(request('events'))).error.code, 'operation_failed'); // events needs a reporter
    assert.equal((await g.invoke(request('ask'))).error.code, 'operation_failed'); // ask needs a reverse caller
    assert.deepEqual((await g.invoke(request('session'))).payload, { session: false, concurrent: false, answer: null });
});

test('the hello may leave out the plugin, the contract and the surface', async () => {
    const hello = extra => ({ apiVersion: 'ext.plugin/v1', id: 'hello', operation: 'plugin.hello', deadline: new Date(Date.now() + 30000).toISOString(), ...extra });
    for (const [what, frame, accepted] of [
        ['a bare hello, as the C engine sends it', hello(), true],
        ['empty members', hello({ plugin: { id: '', revision: '' }, contract: { name: '', version: '' }, surface: '' }), true],
        ['a plugin named', hello({ plugin: { id: 'x', revision: '' } }), false],
        ['a contract named', hello({ contract: { name: 'x', version: '' } }), false],
        ['a surface named', hello({ surface: 'x' }), false],
        ['a payload', hello({ payload: {} }), false],
        ['a null plugin', hello({ plugin: null }), false],
        ['an unknown member', hello({ extra: 1 }), false],
        ['a deadline in the past', hello({ deadline: new Date(Date.now() - 1000).toISOString() }), false]
    ]) {
        const host = new Host({ hello: false });
        host.send(frame);
        if (accepted) {
            assert.equal((await host.recv()).id, 'hello', what);
            assert.equal(await host.finish(), null, what);
        } else {
            assert.ok(await host.finish() instanceof Error, what);
            assert.equal(await host.tryRecv(50), null, what);
        }
    }
});
