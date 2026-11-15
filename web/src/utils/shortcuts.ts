// 键盘快捷键的默认值、匹配与本地持久化。
//
// 输入：KeyboardEvent 或用户录制的键位字符串（如 "Ctrl+A"、"F2"、"Space"）
// 输出：匹配结果、规范化后的键位、冲突动作
// 预期：键位串存 localStorage；缺失/损坏时回落到默认值，不影响列表可用
import type { KeyboardEvent as ReactKeyboardEvent } from 'react';

export type ShortcutAction =
  | 'tabOverview'
  | 'tabFiles'
  | 'tabSettings'
  | 'tabDownloads'
  | 'focusSearch'
  | 'moveUp'
  | 'moveDown'
  | 'rename'
  | 'delete'
  | 'selectAll'
  | 'toggleSelect'
  | 'open'
  | 'clearOrUp';

export type ShortcutMap = Record<ShortcutAction, string>;

// SHORTCUT_ACTIONS 是设置页展示顺序：页面级在前，文件操作在后
export const SHORTCUT_ACTIONS: ShortcutAction[] = [
  'tabOverview',
  'tabFiles',
  'tabSettings',
  'tabDownloads',
  'focusSearch',
  'moveUp',
  'moveDown',
  'clearOrUp',
  'open',
  'toggleSelect',
  'selectAll',
  'rename',
  'delete',
];

export const DEFAULT_SHORTCUTS: ShortcutMap = {
  tabOverview: 'Alt+1',
  tabFiles: 'Alt+2',
  tabSettings: 'Alt+3',
  tabDownloads: 'Alt+4',
  focusSearch: '/',
  moveUp: 'ArrowUp',
  moveDown: 'ArrowDown',
  rename: 'F2',
  delete: 'Delete',
  selectAll: 'Ctrl+A',
  toggleSelect: 'Space',
  open: 'Enter',
  clearOrUp: 'Escape',
};

const STORAGE_KEY = 'mytgfs_shortcuts_v1';

type KeyLike = Pick<ReactKeyboardEvent, 'key' | 'ctrlKey' | 'metaKey' | 'altKey' | 'shiftKey'>;

// normalizeKey 把浏览器给的 key 统一成可显示的写法。
//
// 输入：KeyboardEvent.key
// 输出：Space/Escape/单字符大写/其余原样；纯修饰键返回空串
// 预期：同一个键在录制与匹配时得到同一个字符串
function normalizeKey(key: string): string {
  if (!key || key === 'Control' || key === 'Shift' || key === 'Alt' || key === 'Meta') return '';
  if (key === ' ' || key === 'Spacebar') return 'Space';
  if (key === 'Esc') return 'Escape';
  if (key.length === 1) return key.toUpperCase();
  return key;
}

// shortcutFromEvent 把一次按键规范化成 "Ctrl+Shift+A" 形式。
//
// 输入：键盘事件（或同名字段）
// 输出：键位串；只按下修饰键时返回空串
// 预期：Ctrl 与 Cmd 统一记作 Ctrl，跨平台同一份配置
export function shortcutFromEvent(e: KeyLike): string {
  const key = normalizeKey(e.key);
  if (!key) return '';
  const parts: string[] = [];
  if (e.ctrlKey || e.metaKey) parts.push('Ctrl');
  if (e.altKey) parts.push('Alt');
  if (e.shiftKey) parts.push('Shift');
  parts.push(key);
  return parts.join('+');
}

// matchesShortcut 判断事件是否命中给定键位。
//
// 输入：键盘事件；spec 形如 "Ctrl+A"、"F2"
// 输出：命中为 true
// 预期：修饰键必须完全一致（多按了 Shift 不算命中）
export function matchesShortcut(e: KeyLike, spec: string): boolean {
  if (!spec) return false;
  const parts = spec.split('+');
  const key = parts.pop() ?? '';
  const mods = new Set(parts);
  if ((e.ctrlKey || e.metaKey) !== mods.has('Ctrl')) return false;
  if (e.altKey !== mods.has('Alt')) return false;
  if (e.shiftKey !== mods.has('Shift')) return false;
  return normalizeKey(e.key) === key;
}

// matchesShortcutWithShift 允许 Shift 作为额外修饰键。
//
// 输入：键盘事件；spec 形如 "ArrowDown"
// 输出：命中为 true
// 预期：spec 没写 Shift 时，多按 Shift 也算命中（用于 Shift+方向键扩展选择）；
//       spec 自己带 Shift 时按精确匹配
export function matchesShortcutWithShift(e: KeyLike, spec: string): boolean {
  if (matchesShortcut(e, spec)) return true;
  if (!e.shiftKey) return false;
  if (spec.split('+').includes('Shift')) return false;
  return matchesShortcut({ ...e, shiftKey: false }, spec);
}

// formatShortcut 把键位转成显示用的短写法。
//
// 输入：spec 如 "ArrowUp"、"Ctrl+A"
// 输出：方向键显示成箭头，其余原样
// 预期：只影响展示，不参与匹配
export function formatShortcut(spec: string): string {
  return spec
    .replace(/\bArrowUp\b/g, '↑')
    .replace(/\bArrowDown\b/g, '↓')
    .replace(/\bArrowLeft\b/g, '←')
    .replace(/\bArrowRight\b/g, '→');
}

// shortcutConflict 找与 spec 重复的其他动作。
//
// 输入：当前映射；要检查的动作与键位
// 输出：冲突的动作名；没有冲突返回 null
// 预期：比较忽略大小写；同一个动作自己不算冲突
export function shortcutConflict(
  map: ShortcutMap,
  action: ShortcutAction,
  spec: string
): ShortcutAction | null {
  const want = spec.toLowerCase();
  for (const other of SHORTCUT_ACTIONS) {
    if (other === action) continue;
    if (map[other].toLowerCase() === want) return other;
  }
  return null;
}

// sanitizeShortcuts 把任意来源的对象收敛成完整映射。
//
// 输入：可能是 null/旧版本/缺字段的对象
// 输出：六个动作齐全的映射；缺的或非字符串用默认值补
// 预期：不抛异常，坏数据不会让列表键盘操作失效
export function sanitizeShortcuts(raw: unknown): ShortcutMap {
  const out: ShortcutMap = { ...DEFAULT_SHORTCUTS };
  if (!raw || typeof raw !== 'object') return out;
  for (const action of SHORTCUT_ACTIONS) {
    const value = (raw as Record<string, unknown>)[action];
    if (typeof value === 'string' && value.trim()) out[action] = value.trim();
  }
  return out;
}

// loadShortcuts 从 localStorage 读取键位；没有或损坏时返回默认值。
export function loadShortcuts(): ShortcutMap {
  if (typeof localStorage === 'undefined') return { ...DEFAULT_SHORTCUTS };
  try {
    const text = localStorage.getItem(STORAGE_KEY);
    if (!text) return { ...DEFAULT_SHORTCUTS };
    return sanitizeShortcuts(JSON.parse(text));
  } catch {
    return { ...DEFAULT_SHORTCUTS };
  }
}

// saveShortcuts 写入 localStorage；写失败（隐私模式）静默忽略。
export function saveShortcuts(map: ShortcutMap): void {
  if (typeof localStorage === 'undefined') return;
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(map));
  } catch {
    /* 存不下就只在本次会话生效 */
  }
}
