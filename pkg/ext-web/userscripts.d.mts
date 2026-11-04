export type UserscriptPlan = { id: string; revision: string; enabled: boolean; registration: { id: string; [key: string]: unknown } };
export function createUserscriptRegistry<T = unknown>(options: {
  native: { getScripts(): Promise<{ id: string }[]>; register(scripts: UserscriptPlan['registration'][]): Promise<void>; update(scripts: UserscriptPlan['registration'][]): Promise<void>; unregister(options: { ids: string[] }): Promise<void> };
  storage: { get(key: string): Promise<Record<string, unknown>>; set(values: Record<string, unknown>): Promise<void> };
  namespace: string;
  prepare(value: T): UserscriptPlan | Promise<UserscriptPlan>;
  authorize(action: 'install' | 'update' | 'remove', next: UserscriptPlan, previous?: UserscriptPlan): void | Promise<void>;
  onChange?(action: 'installed' | 'updated' | 'enabled' | 'disabled' | 'removed', plan: UserscriptPlan): void | Promise<void>;
}): {
  /**
   * The browser registration ID for a script ID: the namespaced prefix, then the ID with `.` and `_`
   * escaped to three characters each. Throws an error with code `invalid` when the ID is not valid or the
   * result would exceed 128 characters. The portable userscript rules allow an ID of 128 characters, so
   * the longest ID the registry can register depends on its namespace; call this from `prepare` to refuse
   * such a script before it is reviewed.
   */
  registrationId(id: string): string;
  availability(): Promise<boolean>; list(): Promise<UserscriptPlan[]>;
  install(value: T): Promise<UserscriptPlan>; update(value: T): Promise<UserscriptPlan>;
  setEnabled(id: string, enabled: boolean): Promise<UserscriptPlan>;
  remove(id: string): Promise<void>;
  reconcile(): Promise<{ restored: number; removed: number }>;
};
