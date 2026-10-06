// Run by the Go host below; protocol stdout is reserved for JSON-line frames.
import { Registry, schemaCodec, serveGuest } from '../../pkg/plugin-ts/index.mjs';
import { nodeIO } from '../../pkg/plugin-ts/node.mjs';
const codec = schemaCodec({ type: 'object', properties: { message: { type: 'string', maxLength: 256 } }, required: ['message'] });
const method = { contract: { name: 'example.echo', version: 'v1' }, operation: { name: 'echo', surface: 'observation' }, input: codec, output: codec };
const registry = new Registry({ id: 'example/typescript', revision: 'compiled-1' }).register(method, input => input);
const guest = registry.guest({ authorize(request) { if (request.operation !== 'echo')
        throw new Error('denied'); } });
await serveGuest(nodeIO(process.stdin, process.stdout), guest);
