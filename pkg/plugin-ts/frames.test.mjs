// The shared fixtures for progress events and reverse calls (lib/plugin/testdata/v1),
// which the Go contract computes and every SDK must reproduce: frames, the sender's
// rate limiter and the wire model. A case this SDK judges differently is listed in
// `DIFFERENCES` with the reason, and the list is checked both ways.
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { decodeJSON, validateDescriptor, validateRequest, lookup, validateProgressFrame, validateReverseRequest, validateReverseResponse, Limiter } from './index.mjs';

const fixture = async name => JSON.parse(await readFile(new URL(`../../lib/plugin/testdata/v1/${name}`, import.meta.url)));
const accepts = judge => { try { judge(); return true; } catch { return false; } };

// JSON.parse keeps no trace of how a number was written, and this SDK refuses
// integers beyond 2^53 (a payload number must survive a round trip): so a count of
// 1.0 reads as 1, and 2^64-1 is refused where Go accepts it.
const DIFFERENCES = {
    progress: new Map([
        ['progress: hand-made 13', 'a count written 1.0 reads as 1'],
        ['progress: hand-made 14', 'a count of 2^64-1 is beyond 2^53'],
    ]),
    reverseResponse: new Map([
        ['object payload', 'a payload number beyond 2^53 (and 1.10 reads as 1.1)'],
    ]),
    reverseRequest: new Map([
        ['reverse request: mutation 192', 'a payload number beyond 2^53'],
        ['reverse request: mutation 211', 'a payload number beyond 2^53'],
    ]),
    request: new Map([
        ['mutation 154', 'a payload number beyond 2^53'],
        ['mutation 173', 'a payload number beyond 2^53'],
    ]),
};

function compare(section, cases, judge, differences = new Map()) {
    const mismatched = [];
    for (const c of cases) {
        const got = judge(c);
        if (got !== c.valid) mismatched.push(c.name);
    }
    const expected = [...differences.keys()].filter(name => cases.some(c => c.name === name)).sort();
    assert.deepEqual(mismatched.sort(), expected, `${section}: cases judged differently from Go`);
}

test('frames are judged as the Go contract judges them', async () => {
    const root = await fixture('frame-cases.json');
    for (const [section, judge] of [
        ['progress', c => accepts(() => validateProgressFrame(decodeJSON(c.frame)))],
        ['reverseRequest', c => accepts(() => validateReverseRequest(decodeJSON(c.frame)))],
        ['reverseResponse', c => accepts(() => validateReverseResponse(decodeJSON(c.frame), c.id))],
    ]) {
        const cases = root[section];
        const valid = cases.filter(c => c.valid).length;
        assert.ok(cases.length >= 20 && valid >= 5 && cases.length - valid >= 5, `${section} proves little`);
        compare(section, cases, judge, DIFFERENCES[section]);
    }
});

test('the rate limiter reproduces every step', async () => {
    const root = await fixture('progress-limiter-cases.json');
    assert.ok(root.cases.length >= 10);
    for (const c of root.cases) {
        const limiter = new Limiter({ eventsPerSecond: c.eventsPerSecond, bytesPerSecond: c.bytesPerSecond }, 0);
        c.steps.forEach((step, i) => {
            const dropped = limiter.admit(step.textBytes, step.atMs * 1e6);
            assert.equal(dropped !== null, step.admitted, `${c.name}: step ${i}`);
            if (step.admitted) assert.equal(dropped, step.dropped ?? 0, `${c.name}: step ${i}`);
        });
        assert.equal(limiter.pending(), c.pending, `${c.name}: pending`);
    }
});

test('the wire model is judged as the Go contract judges it', async () => {
    const root = await fixture('model-cases.json');
    assert.ok(root.descriptors.length > 100 && root.requests.length > 100);
    compare('descriptor', root.descriptors, c => accepts(() => validateDescriptor(decodeJSON(c.descriptor))));
    const selected = root.requestDescriptor;
    validateDescriptor(selected);
    compare('request', root.requests, c => accepts(() => validateRequest(selected, decodeJSON(c.request))), DIFFERENCES.request);
    // A valid request gets a reporter when its operation declared progress and the
    // request carries a rate, at the rate with defaults filled in.
    for (const c of root.requests.filter(c => c.valid && !DIFFERENCES.request.has(c.name))) {
        const r = decodeJSON(c.request);
        const declared = lookup(selected, r.contract, r.operation).progress === true;
        const reporter = declared && r.progress != null;
        assert.equal(reporter, c.reporter ?? false, `${c.name}: reporter`);
        if (reporter) {
            assert.equal(r.progress.eventsPerSecond > 0 ? r.progress.eventsPerSecond : 50, c.events, `${c.name}: events`);
            assert.equal(r.progress.bytesPerSecond > 0 ? r.progress.bytesPerSecond : 65536, c.bytes, `${c.name}: bytes`);
        }
    }
});
