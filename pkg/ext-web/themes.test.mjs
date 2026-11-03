import test from 'node:test';
import assert from 'node:assert/strict';
import { createTheme, createThemeController, createThemeManifest } from './themes.mjs';

test('theme manifest uses portable RGB colors and packaged images', () => {
  const colors = { frame: [4, 8, 16], tab_background_text: [255, 255, 255] };
  const manifest = createThemeManifest({ name: 'Night', version: '1.0.0', geckoId: 'night@example.org', colors, images: { theme_frame: 'images/night.png' } });
  colors.frame[0] = 99;
  assert.equal(manifest.theme.colors.frame[0], 4);
  assert.equal(manifest.browser_specific_settings.gecko.id, 'night@example.org');
  assert.deepEqual(manifest.theme.images, { theme_frame: 'images/night.png' });
  assert.throws(() => createTheme({ colors: { frame: '#000000' } }), { code: 'invalid' });
  assert.throws(() => createTheme({ colors: { frame: [0, 0, 0] }, images: { theme_frame: '../remote.png' } }), { code: 'invalid' });
  assert.throws(() => createTheme({ colors: { frame: [0, 0, 0] }, images: { additional_backgrounds: 'image.png' } }), { code: 'invalid' });
  assert.throws(() => createThemeManifest({ name: 'Invalid', version: 'wrong', colors }), { code: 'invalid' });
});

test('live theme controller calls native API with optional window scope', async () => {
  const calls = [];
  const controller = createThemeController({
    getCurrent(...args) { calls.push(['current', ...args]); return Promise.resolve({ colors: {} }); },
    update(...args) { calls.push(['update', ...args]); return Promise.resolve(); },
    reset(...args) { calls.push(['reset', ...args]); return Promise.resolve(); },
  });
  const input = { colors: { frame: [1, 2, 3] } };
  await controller.apply(input);
  await controller.apply(input, 7);
  await controller.current(7);
  await controller.reset();
  assert.deepEqual(calls, [
    ['update', { colors: { frame: [1, 2, 3] } }],
    ['update', 7, { colors: { frame: [1, 2, 3] } }],
    ['current', 7], ['reset'],
  ]);
  assert.throws(() => controller.apply(input, -1), { code: 'invalid' });
  assert.throws(() => createThemeController({}).apply(input), { code: 'unavailable' });
});
