import { useEffect, useState } from 'react';
import type { ThemeMode } from '../types';
import { STORAGE_KEYS, loadFromStorage, saveToStorage } from '../utils/storage';

export function useThemeMode() {
  const [theme, setTheme] = useState<ThemeMode>(() => {
    const saved = loadFromStorage<ThemeMode | null>(STORAGE_KEYS.theme, null);
    // 旧版本只存过 light/dark：读到非法值回落到跟随系统
    return saved === 'light' || saved === 'dark' || saved === 'system' ? saved : 'system';
  });

  useEffect(() => {
    const root = document.documentElement;
    saveToStorage(STORAGE_KEYS.theme, theme);
    if (theme !== 'system') {
      root.classList.toggle('dark', theme === 'dark');
      return;
    }
    const media = window.matchMedia('(prefers-color-scheme: dark)');
    const apply = () => root.classList.toggle('dark', media.matches);
    apply();
    media.addEventListener('change', apply);
    return () => media.removeEventListener('change', apply);
  }, [theme]);

  return { theme, setTheme };
}
