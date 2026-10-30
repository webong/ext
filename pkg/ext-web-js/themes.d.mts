export type RGB = [number, number, number];
export type ThemeInput = { colors: Record<string, RGB>; images?: Record<string, string | string[]> };
export type Theme = { colors: Record<string, RGB>; images?: Record<string, string | string[]> };
export function createTheme(input: ThemeInput): Theme;
export function createThemeManifest(input: ThemeInput & { name: string; version: string; geckoId?: string }): {
  manifest_version: 3; name: string; version: string; theme: Theme;
  browser_specific_settings?: { gecko: { id: string } };
};
export function createThemeController(native?: {
  getCurrent?(windowId?: number): Promise<unknown>;
  update?(theme: Theme): Promise<void>; update?(windowId: number, theme: Theme): Promise<void>;
  reset?(windowId?: number): Promise<void>;
}): {
  current(windowId?: number): Promise<unknown>;
  apply(theme: ThemeInput, windowId?: number): Promise<void>;
  reset(windowId?: number): Promise<void>;
};
