import { PeerError } from './index.mjs';

const object = value => value && typeof value === 'object' && !Array.isArray(value);
const label = (value, field) => {
  if (typeof value !== 'string' || !value.trim() || value.length > 128) throw new PeerError('invalid', `Invalid ${field}`);
  return value.trim();
};
const windowId = value => {
  if (value !== undefined && (!Number.isSafeInteger(value) || value < 0)) throw new PeerError('invalid', 'Invalid window ID');
  return value;
};
const asset = value => {
  if (typeof value !== 'string' || value.length > 256 || !value || value.startsWith('/') || value.includes('\\') || value.includes('?') || value.includes('#') || value.split('/').some(part => !part || part === '.' || part === '..') || /^[a-z][a-z\d+.-]*:/i.test(value)) throw new PeerError('invalid', 'Theme images must be packaged relative paths');
  return value;
};

/** A portable color-and-image theme for static manifests or supported live theme APIs. */
export function createTheme({ colors, images } = {}) {
  if (!object(colors) || !Object.keys(colors).length || Object.keys(colors).length > 64) throw new PeerError('invalid', 'Theme colors are required');
  const checked = { colors: {} };
  for (const [name, rgb] of Object.entries(colors)) {
    if (!/^[a-z][a-z0-9_]{0,63}$/.test(name) || !Array.isArray(rgb) || rgb.length !== 3 || rgb.some(value => !Number.isInteger(value) || value < 0 || value > 255)) throw new PeerError('invalid', `Invalid theme color: ${name}`);
    checked.colors[name] = [...rgb];
  }
  if (images !== undefined) {
    if (!object(images) || !Object.keys(images).length || Object.keys(images).length > 16) throw new PeerError('invalid', 'Invalid theme images');
    checked.images = {};
    for (const [name, path] of Object.entries(images)) {
      if (!/^[a-z][a-z0-9_]{0,63}$/.test(name)) throw new PeerError('invalid', `Invalid theme image: ${name}`);
      if (name === 'additional_backgrounds') {
        if (!Array.isArray(path) || !path.length || path.length > 16) throw new PeerError('invalid', 'Invalid additional theme backgrounds');
        checked.images[name] = path.map(asset);
      } else checked.images[name] = asset(path);
    }
  }
  return checked;
}

/** Build a static browser theme manifest; the caller supplies packaged image files. */
export function createThemeManifest({ name, version, colors, images, geckoId } = {}) {
  const checkedVersion = label(version, 'theme version');
  if (!/^\d+(?:\.\d+){0,3}$/.test(checkedVersion)) throw new PeerError('invalid', 'Invalid theme version');
  const manifest = { manifest_version: 3, name: label(name, 'theme name'), version: checkedVersion, theme: createTheme({ colors, images }) };
  if (geckoId !== undefined) {
    if (typeof geckoId !== 'string' || !/^[a-zA-Z0-9._-]{1,64}@[a-zA-Z0-9.-]{1,63}$/.test(geckoId)) throw new PeerError('invalid', 'Invalid Gecko ID');
    manifest.browser_specific_settings = { gecko: { id: geckoId } };
  }
  return manifest;
}

/** Apply and observe live themes where the browser exposes its theme API. */
export function createThemeController(native = globalThis.browser?.theme ?? globalThis.chrome?.theme) {
  if (!native) throw new PeerError('unavailable', 'Live theme API unavailable');
  return {
    current(id) {
      windowId(id);
      if (!native.getCurrent) throw new PeerError('unavailable', 'Current theme unavailable');
      return id === undefined ? native.getCurrent() : native.getCurrent(id);
    },
    apply(input, id) {
      windowId(id);
      if (!native.update) throw new PeerError('unavailable', 'Live theme updates unavailable');
      const theme = createTheme(input);
      return id === undefined ? native.update(theme) : native.update(id, theme);
    },
    reset(id) {
      windowId(id);
      if (!native.reset) throw new PeerError('unavailable', 'Live theme reset unavailable');
      return id === undefined ? native.reset() : native.reset(id);
    },
  };
}
