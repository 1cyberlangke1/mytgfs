
/** 面板持久化的键名；版本后缀让旧格式自然失效，不需要迁移代码。 */
export const STORAGE_KEYS = {
  tab: 'mytgfs_active_tab_v1',
  locale: 'mytgfs_locale_v1',
  theme: 'mytgfs_theme_v1',
  activeVol: 'mytgfs_active_vol_v1',
} as const;

export function loadFromStorage<T>(key: string, fallback: T): T {
  if (typeof window === 'undefined') return fallback;
  try {
    const raw = window.localStorage.getItem(key);
    if (!raw) return fallback;
    return JSON.parse(raw) as T;
  } catch {
    return fallback;
  }
}

export function saveToStorage(key: string, value: unknown): void {
  if (typeof window === 'undefined') return;
  try {
    window.localStorage.setItem(key, JSON.stringify(value));
  } catch {
    // 存储不可用时偏好无法持久化，但当前会话仍然生效
  }
}

export function urlParam(name: string): string {
  if (typeof window === 'undefined') return '';
  return new URLSearchParams(window.location.search).get(name) ?? '';
}
