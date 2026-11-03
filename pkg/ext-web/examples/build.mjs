import { cp, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { createThemeManifest } from '../themes.mjs';
const root = dirname(dirname(fileURLToPath(import.meta.url)));
for (const name of ['host', 'guest']) {
  const target = join(root, 'dist', name);
  await rm(target, { recursive: true, force: true });
  await mkdir(target, { recursive: true });
  await cp(join(root, 'examples', name), target, { recursive: true });
  await cp(join(root, 'index.mjs'), join(target, 'peer.mjs'));
}
const themeTarget = join(root, 'dist', 'theme');
await rm(themeTarget, { recursive: true, force: true });
await mkdir(themeTarget, { recursive: true });
await writeFile(join(themeTarget, 'manifest.json'), `${JSON.stringify(createThemeManifest({
  name: 'Ext Example Theme', version: '0.1.0', geckoId: 'ext-example-theme@example.org',
  colors: { frame: [32, 42, 68], toolbar: [45, 56, 82], tab_background_text: [245, 247, 255], bookmark_text: [245, 247, 255] },
}), null, 2)}\n`);
for (const name of ['host', 'guest']) {
  const target = join(root, 'dist', `firefox-${name}`);
  await rm(target, { recursive: true, force: true });
  await cp(join(root, 'dist', name), target, { recursive: true });
  await cp(join(root, 'examples', `firefox-${name}`, 'manifest.json'), join(target, 'manifest.json'));
  for (const file of ['background.mjs', 'popup.js']) {
    const path = join(target, file);
    const source = await readFile(path, 'utf8');
    await writeFile(path, source.replaceAll('chrome.', 'browser.'));
  }
}
