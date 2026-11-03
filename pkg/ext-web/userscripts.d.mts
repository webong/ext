export type UserscriptPlan = { id: string; revision: string; enabled: boolean; registration: { id: string; [key: string]: unknown } };
export function createUserscriptRegistry<T = unknown>(options: {
  native: { getScripts(): Promise<{ id: string }[]>; register(scripts: UserscriptPlan['registration'][]): Promise<void>; update(scripts: UserscriptPlan['registration'][]): Promise<void>; unregister(options: { ids: string[] }): Promise<void> };
  storage: { get(key: string): Promise<Record<string, unknown>>; set(values: Record<string, unknown>): Promise<void> };
  namespace: string;
  prepare(value: T): UserscriptPlan | Promise<UserscriptPlan>;
  authorize(action: 'install' | 'update' | 'remove', next: UserscriptPlan, previous?: UserscriptPlan): void | Promise<void>;
  onChange?(action: 'installed' | 'updated' | 'enabled' | 'disabled' | 'removed', plan: UserscriptPlan): void | Promise<void>;
}): {
  registrationId(id: string): string;
  availability(): Promise<boolean>; list(): Promise<UserscriptPlan[]>;
  install(value: T): Promise<UserscriptPlan>; update(value: T): Promise<UserscriptPlan>;
  setEnabled(id: string, enabled: boolean): Promise<UserscriptPlan>;
  remove(id: string): Promise<void>;
  reconcile(): Promise<{ restored: number; removed: number }>;
};
