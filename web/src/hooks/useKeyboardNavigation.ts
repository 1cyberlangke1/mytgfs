import { useEffect, useRef } from 'react';
import { matchesShortcut, type ShortcutMap } from '../utils/shortcuts';
import {
  activateActive,
  focusAdjacent,
  focusPrimaryNav,
  moveFocusDirection,
  scrollActiveByPage,
  scrollActiveToEdge,
} from '../utils/keynav';
import { isTextField, markEditable } from '../utils/formEditing';

export function useKeyboardNavigation(shortcuts: ShortcutMap) {
  // 两段式状态：指向当前正在编辑的控件，未编辑时为 null
  const editingControl = useRef<HTMLElement | null>(null);

  // 指针与焦点的进出决定「此刻是不是在编辑」，不依赖输入框自身的事件冒泡
  useEffect(() => {
    const onPointerDown = (e: PointerEvent) => {
      const hit =
        (e.target as HTMLElement | null)?.closest<HTMLElement>('input, textarea, select') ?? null;
      editingControl.current = isTextField(hit) || hit?.tagName === 'SELECT' ? hit : null;
      markEditable(hit, editingControl.current !== null);
    };
    // 键盘走到输入框时先只读：字符要等 Enter 才进来，读屏也能据此播报
    const onFocusIn = (e: FocusEvent) => {
      const el = e.target as Element | null;
      if (!isTextField(el as HTMLElement | null)) return;
      if (el?.closest('dialog[open]')) return;
      markEditable(el, el === editingControl.current);
    };
    const onFocusOut = (e: FocusEvent) => {
      if (e.target === editingControl.current) editingControl.current = null;
      markEditable(e.target as Element | null, false);
    };
    document.addEventListener('pointerdown', onPointerDown, true);
    document.addEventListener('focusin', onFocusIn, true);
    document.addEventListener('focusout', onFocusOut, true);
    return () => {
      document.removeEventListener('pointerdown', onPointerDown, true);
      document.removeEventListener('focusin', onFocusIn, true);
      document.removeEventListener('focusout', onFocusOut, true);
    };
  }, []);

  // 全局键盘：方向键移动光标，Enter 确认/进入编辑，Esc 逐层退出
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented) return;
      if (e.ctrlKey || e.altKey || e.metaKey) return;
      const target = e.target as HTMLElement | null;
      const tag = target?.tagName ?? '';

      const dir = matchesShortcut(e, shortcuts.moveUp)
        ? 'up'
        : matchesShortcut(e, shortcuts.moveDown)
          ? 'down'
          : matchesShortcut(e, shortcuts.moveLeft)
            ? 'left'
            : matchesShortcut(e, shortcuts.moveRight)
              ? 'right'
              : null;

      if (document.querySelector('dialog[open]')) return;

      if (isTextField(target)) {
        if (!target) return;
        // 只有回车/点击进入过的输入框才接受文字：否则方向键一落到搜索框，
        // 整片键盘就被它吃掉（光标在框内跳，走不出去）
        if (target !== editingControl.current) {
          if (dir) {
            if (moveFocusDirection(dir)) e.preventDefault();
            return;
          }
          if (matchesShortcut(e, shortcuts.confirm)) {
            e.preventDefault();
            editingControl.current = target;
            markEditable(target, true);
            return;
          }
          if (matchesShortcut(e, shortcuts.back)) {
            if (!focusAdjacent(target, 1)) focusPrimaryNav();
            return;
          }
          // 未进入编辑前，普通字符不许落进输入框
          e.preventDefault();
          return;
        }
        if (matchesShortcut(e, shortcuts.back)) {
          // 退出编辑但保留焦点：再按一次 Esc 才离开这个框
          editingControl.current = null;
          markEditable(target, false);
        }
        return;
      }

      if (tag === 'SELECT') {
        if (!target) return;
        // 与文本框同一套两段式：光标落在下拉上时方向键继续走，
        // Enter 才进入「改选项」，否则上下键会把焦点永远锁在这一项里
        if (target !== editingControl.current) {
          if (dir) {
            if (moveFocusDirection(dir)) e.preventDefault();
            return;
          }
          if (matchesShortcut(e, shortcuts.confirm)) {
            e.preventDefault();
            editingControl.current = target;
            return;
          }
          if (matchesShortcut(e, shortcuts.back)) {
            if (!focusAdjacent(target, 1)) focusPrimaryNav();
            return;
          }
          return;
        }
        if (matchesShortcut(e, shortcuts.back)) {
          // 退出改选项，焦点留在下拉上，再按一次 Esc 才离开
          editingControl.current = null;
        }
        return;
      }

      if (dir) {
        if (moveFocusDirection(dir)) e.preventDefault();
        return;
      }
      if (matchesShortcut(e, shortcuts.prevPage) && scrollActiveByPage(-1)) {
        e.preventDefault();
        return;
      }
      if (matchesShortcut(e, shortcuts.nextPage) && scrollActiveByPage(1)) {
        e.preventDefault();
        return;
      }
      if (matchesShortcut(e, shortcuts.home) && scrollActiveToEdge('start')) {
        e.preventDefault();
        return;
      }
      if (matchesShortcut(e, shortcuts.end) && scrollActiveToEdge('end')) {
        e.preventDefault();
        return;
      }
      if (matchesShortcut(e, shortcuts.confirm) && activateActive()) {
        e.preventDefault();
        return;
      }
      if (matchesShortcut(e, shortcuts.back)) {
        // 页面自己的 Esc 处理在这之后才跑，用微任务确认没人接再回导航
        queueMicrotask(() => {
          if (e.defaultPrevented) return;
          focusPrimaryNav();
        });
      }
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [shortcuts]);
}
