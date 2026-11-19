// 设置页的纯计算：字段标签、分组、校验与快捷键动作说明。
//
// 输入：schema 定义、键名与文案
// 输出：显示名、说明、分组顺序与校验错误
// 预期：只依赖传入参数，不发请求、不碰 DOM，便于单测与复用
import type { AppSettingDef } from '../types';
import type { Translations } from './i18n';
import type { ShortcutAction } from './shortcuts';

// shortcutLabel 取动作的中文/英文说明，与全局键盘模型一致。
export function shortcutLabel(t: Translations, action: ShortcutAction): string {
  switch (action) {
    case 'moveUp':
      return t.files.shortcutMoveUp;
    case 'moveDown':
      return t.files.shortcutMoveDown;
    case 'moveLeft':
      return t.files.shortcutMoveLeft;
    case 'moveRight':
      return t.files.shortcutMoveRight;
    case 'confirm':
      return t.files.shortcutConfirm;
    case 'back':
      return t.files.shortcutBack;
    case 'prevPage':
      return t.files.shortcutPrevPage;
    case 'nextPage':
      return t.files.shortcutNextPage;
    case 'home':
      return t.files.shortcutHome;
    case 'end':
      return t.files.shortcutEnd;
    case 'toggleSelect':
      return t.files.shortcutSelect;
    case 'selectAll':
      return t.files.shortcutSelectAll;
    case 'rename':
      return t.files.shortcutRename;
    case 'delete':
      return t.files.shortcutDelete;
  }
}

// SECTION_ORDER 控制分组顺序：与 config.toml 的段落顺序一致。
export const SECTION_ORDER = ['telegram', 'mount', 'downloads', 'log'];

// PANEL_HIDDEN 是不在面板里出现的键：端口只能在启动参数或 config.toml 里改。
export const PANEL_HIDDEN = new Set(['web.port']);

// sectionOf 取 "mount.sync_seconds" 的 "mount"。
export function sectionOf(key: string): string {
  return key.includes('.') ? key.slice(0, key.indexOf('.')) : key;
}

// labelOf 取字段名，缺翻译时回落到键名。
export function labelOf(t: Translations, key: string): string {
  const labels = t.settingLabels as Record<string, string | undefined>;
  return labels[key] ?? key;
}

// hintOf 取字段说明，没有时返回空串。
export function hintOf(t: Translations, key: string): string {
  const hints = t.settingHints as Record<string, string | undefined>;
  return hints[key] ?? '';
}

// sectionTitle 取分组标题，缺翻译时回落到段名。
export function sectionTitle(t: Translations, section: string): string {
  const titles = t.settingSections as Record<string, string | undefined>;
  return titles[section] ?? section;
}

// validateOne 返回该键的非法原因；合法返回空串。
//
// 输入：字段定义、用户输入、文案
// 输出：可读错误；合法返回空串
// 预期：int 先要求整数再查区间；枚举必须命中 options；字符串上限 64
export function validateOne(def: AppSettingDef, value: string, t: Translations): string {
  const label = labelOf(t, def.key);
  if (def.kind === 'int') {
    const n = Number(value);
    if (value.trim() === '' || !Number.isFinite(n) || !Number.isInteger(n)) {
      return t.settings.invalidNumber.replace('{field}', label);
    }
    if (def.min !== undefined && def.max !== undefined && (n < def.min || n > def.max)) {
      return t.settings.invalidRange
        .replace('{field}', label)
        .replace('{min}', String(def.min))
        .replace('{max}', String(def.max));
    }
    return '';
  }
  if (def.options && def.options.length > 0) {
    return def.options.includes(value)
      ? ''
      : t.settings.invalidOption
          .replace('{field}', label)
          .replace('{options}', def.options.join(' / '));
  }
  if (value.length > 64) {
    return t.settings.invalidTooLong.replace('{field}', label);
  }
  return '';
}
