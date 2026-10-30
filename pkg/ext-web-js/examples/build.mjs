import { cp, mkdir, rm } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
const root = dirname(dirname(fileURLToPath(import.meta.url)));
for (const name of ['host', 'guest']) {
  const target = join(root, 'dist', name);
  await rm(target, { recursive: true, force: true });
  await mkdir(target, { recursive: true });
  await cp(join(root, 'examples', name), target, { recursive: true });
  await cp(join(root, 'index.mjs'), join(target, 'peer.mjs'));
}
