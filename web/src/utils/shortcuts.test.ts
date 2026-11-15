import { describe, expect, it } from 'vitest';
import {
  DEFAULT_SHORTCUTS,
  formatShortcut,
  loadShortcuts,
  matchesShortcut,
  matchesShortcutWithShift,
  sanitizeShortcuts,
  saveShortcuts,
  shortcutConflict,
  shortcutFromEvent,
} from './shortcuts';

// ev 造一个只含匹配所需字段的键盘事件
function ev(key: string, mods: Partial<{ ctrlKey: boolean; metaKey: boolean; altKey: boolean; shiftKey: boolean }> = {}) {
  return { key, ctrlKey: false, metaKey: false, altKey: false, shiftKey: false, ...mods };
}

describe('shortcutFromEvent', () => {
  it('普通键与空格、Esc 规范化', () => {
    expect(shortcutFromEvent(ev('F2'))).toBe('F2');
    expect(shortcutFromEvent(ev(' '))).toBe('Space');
    expect(shortcutFromEvent(ev('Esc'))).toBe('Escape');
    expect(shortcutFromEvent(ev('Delete'))).toBe('Delete');
  });

  it('组合键按 Ctrl/Alt/Shift 顺序拼接', () => {
    expect(shortcutFromEvent(ev('a', { ctrlKey: true }))).toBe('Ctrl+A');
    expect(shortcutFromEvent(ev('k', { ctrlKey: true, shiftKey: true }))).toBe('Ctrl+Shift+K');
    expect(shortcutFromEvent(ev('x', { metaKey: true, altKey: true }))).toBe('Ctrl+Alt+X');
  });

  it('只按修饰键返回空串', () => {
    expect(shortcutFromEvent(ev('Control', { ctrlKey: true }))).toBe('');
    expect(shortcutFromEvent(ev('Shift', { shiftKey: true }))).toBe('');
  });
});

describe('matchesShortcut', () => {
  it('单键与组合键命中', () => {
    expect(matchesShortcut(ev('F2'), 'F2')).toBe(true);
    expect(matchesShortcut(ev('a', { ctrlKey: true }), 'Ctrl+A')).toBe(true);
    expect(matchesShortcut(ev(' ', {}), 'Space')).toBe(true);
  });

  it('修饰键必须完全一致', () => {
    expect(matchesShortcut(ev('a'), 'Ctrl+A')).toBe(false);
    expect(matchesShortcut(ev('a', { ctrlKey: true, shiftKey: true }), 'Ctrl+A')).toBe(false);
    expect(matchesShortcut(ev('Escape', { altKey: true }), 'Escape')).toBe(false);
  });

  it('大小写与空键位不命中', () => {
    expect(matchesShortcut(ev('f'), 'F')).toBe(true);
    expect(matchesShortcut(ev('F2'), '')).toBe(false);
  });
});

describe('matchesShortcutWithShift', () => {
  it('多按 Shift 也算命中，除非 spec 自己带 Shift', () => {
    expect(matchesShortcutWithShift(ev('ArrowDown', { shiftKey: true }), 'ArrowDown')).toBe(true);
    expect(matchesShortcutWithShift(ev('ArrowDown'), 'ArrowDown')).toBe(true);
    expect(matchesShortcutWithShift(ev('j', { shiftKey: true }), 'Shift+J')).toBe(true);
    expect(matchesShortcutWithShift(ev('j'), 'Shift+J')).toBe(false);
  });
});

describe('formatShortcut', () => {
  it('方向键显示成箭头，其余原样', () => {
    expect(formatShortcut('ArrowUp')).toBe('↑');
    expect(formatShortcut('ArrowDown')).toBe('↓');
    expect(formatShortcut('Ctrl+A')).toBe('Ctrl+A');
  });
});

describe('shortcutConflict', () => {
  it('同键位被别的动作占用时报出来', () => {
    expect(shortcutConflict(DEFAULT_SHORTCUTS, 'rename', 'Delete')).toBe('delete');
    expect(shortcutConflict(DEFAULT_SHORTCUTS, 'rename', 'Ctrl+A')).toBe('selectAll');
  });

  it('自己占用自己不算冲突，大小写不敏感', () => {
    expect(shortcutConflict(DEFAULT_SHORTCUTS, 'delete', 'Delete')).toBeNull();
    expect(shortcutConflict(DEFAULT_SHORTCUTS, 'rename', 'DElEtE')).toBe('delete');
  });
});

describe('sanitizeShortcuts', () => {
  it('缺字段用默认值补齐', () => {
    expect(sanitizeShortcuts({ rename: 'F3' })).toEqual({ ...DEFAULT_SHORTCUTS, rename: 'F3' });
  });

  it('坏数据回落到默认值', () => {
    expect(sanitizeShortcuts(null)).toEqual(DEFAULT_SHORTCUTS);
    expect(sanitizeShortcuts('oops')).toEqual(DEFAULT_SHORTCUTS);
    expect(sanitizeShortcuts({ delete: 3, open: '   ' })).toEqual(DEFAULT_SHORTCUTS);
  });
});

describe('loadShortcuts / saveShortcuts', () => {
  it('没有 localStorage 时不抛异常并回落到默认值', () => {
    expect(typeof localStorage).toBe('undefined');
    expect(loadShortcuts()).toEqual(DEFAULT_SHORTCUTS);
    expect(() => saveShortcuts(DEFAULT_SHORTCUTS)).not.toThrow();
  });
});
