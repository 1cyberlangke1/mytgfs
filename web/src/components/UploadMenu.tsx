import React, { useCallback, useEffect, useId, useRef, useState } from 'react';
import { ChevronDown, ClipboardPaste, FileText, FolderUp, Upload } from 'lucide-react';
import type { Translations } from '../utils/i18n';
import { menuPosition } from '../utils/menuPosition';
import { Button } from './ui';

// 原生 focusgroup 提供菜单内的方向键导航（Chrome/Edge 150+）。
// 不支持时用显式 role + keydown 兜底；两者不能同时启用，
// 因为显式 role="menu" 会阻断 focusgroup 的自动角色推断。
const HAS_FOCUSGROUP =
  typeof HTMLElement !== 'undefined' && 'focusGroup' in HTMLElement.prototype;
const GROUP_ROLE = HAS_FOCUSGROUP ? undefined : ('menu' as const);
const ITEM_ROLE = HAS_FOCUSGROUP ? undefined : ('menuitem' as const);
// focusgroup 是新的浏览器属性，React 的类型定义还没收录；
// 用 Record 展开绕过 JSX 的已知属性校验，运行期原样写到 DOM 上
const FOCUSGROUP_ATTR: Record<string, string> = { focusgroup: 'menu nomemory' };

// 菜单项统一外观：左侧固定 1rem 图标槽，文字过长省略
const ITEM_CLASS =
  'flex w-full cursor-pointer items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-xs text-zinc-700 transition-colors hover:bg-zinc-100 focus-visible:bg-zinc-100 dark:text-zinc-200 dark:hover:bg-zinc-800/80 dark:focus-visible:bg-zinc-800/80';

// queryItems 取菜单里可聚焦的命令项。
//
// 输入：菜单容器
// 输出：按 DOM 顺序排列的按钮
// 预期：分隔线（hr）不是按钮，天然被排除
function queryItems(menu: HTMLElement): HTMLButtonElement[] {
  return Array.from(menu.querySelectorAll<HTMLButtonElement>('[data-menu-item]'));
}

/**
 * UploadMenuProps 是统一上传入口的入参。
 *
 * 输入：t 文案；onPickFile 系统文件选择；onPickFolder 系统目录选择；
 *       onNewText 新建文本弹窗；onPasteClipboard 读取系统剪贴板
 * 输出：一个按钮 + 原生 popover 菜单
 * 预期：所有「新建内容」的入口收进一个按钮，工具栏只留这一个强调色主操作
 */
export interface UploadMenuProps {
  t: Translations;
  onPickFile: () => void;
  onPickFolder: () => void;
  onNewText: () => void;
  onPasteClipboard: () => void;
}

/**
 * UploadMenu 是文件页的上传主按钮。
 *
 * 输入：见 UploadMenuProps
 * 输出：触发按钮 + 顶层菜单
 * 预期：
 *   - 点击/Enter/Space 开关菜单；ArrowDown 打开并把焦点送进第一项
 *   - 菜单用原生 popover：top-layer、点外部关闭、Esc 关闭并归还焦点都由浏览器负责
 *   - 位置由脚本算（CSS anchor positioning 目前只有 Safari 支持）
 *   - 菜单项激活后收起菜单并把焦点还给触发按钮，键盘用户不会丢位置
 */
export function UploadMenu({
  t,
  onPickFile,
  onPickFolder,
  onNewText,
  onPasteClipboard,
}: UploadMenuProps) {
  const menuId = useId();
  const triggerId = useId();
  const triggerRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const openRef = useRef(false);
  // 键盘打开才把焦点送进第一项；鼠标打开时焦点留在触发按钮上
  const enterOnOpen = useRef(false);
  // 按下触发按钮时菜单是否已经打开：轻量关闭发生在 pointerdown 阶段，
  // 不记住这一点，随后的 click 会把刚被关掉的菜单又打开一次
  const pressedWhenOpen = useRef(false);
  const [open, setOpen] = useState(false);

  // place 把菜单摆到触发按钮旁边。
  //
  // 输入：无（读两个 ref 的实时矩形）
  // 输出：无（写 left/top，并标记已定位）
  // 预期：定位完成后才显示，避免出现一帧位于默认位置的菜单
  const place = useCallback(() => {
    const menu = menuRef.current;
    const trigger = triggerRef.current;
    if (!menu || !trigger) return;
    const point = menuPosition(trigger.getBoundingClientRect(), menu.getBoundingClientRect(), {
      width: window.innerWidth,
      height: window.innerHeight,
    });
    menu.style.left = `${point.left}px`;
    menu.style.top = `${point.top}px`;
    menu.dataset.placed = 'true';
  }, []);

  // 开关状态以浏览器的 toggle 事件为准，保证 aria-expanded 与真实状态一致
  useEffect(() => {
    const menu = menuRef.current;
    if (!menu) return;
    const onToggle = (event: Event) => {
      const next = (event as ToggleEvent).newState === 'open';
      openRef.current = next;
      setOpen(next);
      if (!next) {
        enterOnOpen.current = false;
        return;
      }
      menu.dataset.placed = 'false';
      place();
      if (enterOnOpen.current) {
        enterOnOpen.current = false;
        queryItems(menu)[0]?.focus({ preventScroll: true });
      }
    };
    menu.addEventListener('toggle', onToggle);
    return () => menu.removeEventListener('toggle', onToggle);
  }, [place]);

  // 打开期间页面滚动/窗口缩放：菜单跟着触发按钮走，否则会飘在原地
  useEffect(() => {
    if (!open) return;
    const reposition = () => place();
    window.addEventListener('resize', reposition);
    window.addEventListener('scroll', reposition, true);
    return () => {
      window.removeEventListener('resize', reposition);
      window.removeEventListener('scroll', reposition, true);
    };
  }, [open, place]);

  // 焦点离开菜单系统（Tab 到下一个控件）：收起，别把打开的菜单留在屏幕上
  useEffect(() => {
    if (!open) return;
    const onFocusIn = (event: FocusEvent) => {
      const menu = menuRef.current;
      const trigger = triggerRef.current;
      if (!menu || !trigger) return;
      const target = event.target as Node | null;
      if (!target) return;
      if (menu.contains(target) || trigger.contains(target)) return;
      menu.hidePopover();
    };
    document.addEventListener('focusin', onFocusIn);
    return () => document.removeEventListener('focusin', onFocusIn);
  }, [open]);

  const onTriggerPointerDown = () => {
    pressedWhenOpen.current = openRef.current;
  };

  const onTriggerClick = (event: React.MouseEvent<HTMLButtonElement>) => {
    const menu = menuRef.current;
    if (!menu) return;
    const fromKeyboard = event.detail === 0;
    if (fromKeyboard) {
      pressedWhenOpen.current = false;
    } else if (pressedWhenOpen.current) {
      // 这一下是「关闭」：菜单通常已在 pointerdown 被轻量关闭，这里只补一次
      pressedWhenOpen.current = false;
      if (openRef.current) menu.hidePopover();
      return;
    }
    if (openRef.current) {
      menu.hidePopover();
      return;
    }
    enterOnOpen.current = fromKeyboard;
    menu.showPopover();
  };

  const onTriggerKeyDown = (event: React.KeyboardEvent<HTMLButtonElement>) => {
    if (event.key !== 'ArrowDown') return;
    const menu = menuRef.current;
    if (!menu) return;
    event.preventDefault();
    if (!openRef.current) {
      enterOnOpen.current = true;
      menu.showPopover();
      return;
    }
    queryItems(menu)[0]?.focus({ preventScroll: true });
  };

  // 方向键兜底：仅在浏览器没有原生 focusgroup 时接管
  const onMenuKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    if (HAS_FOCUSGROUP) return;
    const menu = menuRef.current;
    if (!menu) return;
    if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return;
    const items = queryItems(menu);
    if (items.length === 0) return;
    event.preventDefault();
    const current = items.indexOf(document.activeElement as HTMLButtonElement);
    let next: number;
    if (event.key === 'Home') next = 0;
    else if (event.key === 'End') next = items.length - 1;
    else if (current < 0) next = 0;
    else next = (current + (event.key === 'ArrowUp' ? -1 : 1) + items.length) % items.length;
    items[next]?.focus({ preventScroll: true });
  };

  const choose = (action: () => void) => {
    const menu = menuRef.current;
    const trigger = triggerRef.current;
    if (menu && trigger) {
      // 先还焦点再收起，否则焦点会留在已隐藏的内容里
      trigger.focus({ preventScroll: true });
      menu.hidePopover();
    }
    action();
  };

  return (
    <>
      <Button
        ref={triggerRef}
        id={triggerId}
        type="button"
        variant="accent"
        size="sm"
        className="gap-1.5 ps-2.5 pe-2"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={t.files.uploadMenuLabel}
        title={t.files.uploadMenuLabel}
        onPointerDown={onTriggerPointerDown}
        onClick={onTriggerClick}
        onKeyDown={onTriggerKeyDown}
      >
        <Upload className="h-4 w-4" aria-hidden="true" />
        <span>{t.files.uploadMenuLabel}</span>
        <ChevronDown className="h-3 w-3 opacity-80" aria-hidden="true" />
      </Button>

      <div ref={menuRef} id={menuId} popover="auto" className="upload-menu">
        <div
          {...FOCUSGROUP_ATTR}
          role={GROUP_ROLE}
          aria-labelledby={triggerId}
          onKeyDown={onMenuKeyDown}
          className="w-52 rounded-xl border border-zinc-200/90 bg-white p-1 shadow-xl shadow-zinc-950/10 dark:border-zinc-800 dark:bg-zinc-900 dark:shadow-black/40"
        >
          <button
            type="button"
            data-menu-item
            role={ITEM_ROLE}
            className={ITEM_CLASS}
            onClick={() => choose(onPickFile)}
          >
            <Upload className="h-4 w-4 shrink-0 text-zinc-500 dark:text-zinc-400" aria-hidden="true" />
            <span className="truncate">{t.files.uploadFileBtn}</span>
          </button>
          <button
            type="button"
            data-menu-item
            role={ITEM_ROLE}
            className={ITEM_CLASS}
            onClick={() => choose(onPickFolder)}
          >
            <FolderUp className="h-4 w-4 shrink-0 text-zinc-500 dark:text-zinc-400" aria-hidden="true" />
            <span className="truncate">{t.files.uploadDirBtn}</span>
          </button>

          <hr className="my-1 border-zinc-200/70 dark:border-zinc-800" />

          <button
            type="button"
            data-menu-item
            role={ITEM_ROLE}
            className={ITEM_CLASS}
            onClick={() => choose(onNewText)}
          >
            <FileText className="h-4 w-4 shrink-0 text-zinc-500 dark:text-zinc-400" aria-hidden="true" />
            <span className="truncate">{t.files.newTextBtn}</span>
          </button>
          <button
            type="button"
            data-menu-item
            role={ITEM_ROLE}
            className={ITEM_CLASS}
            onClick={() => choose(onPasteClipboard)}
          >
            <ClipboardPaste className="h-4 w-4 shrink-0 text-zinc-500 dark:text-zinc-400" aria-hidden="true" />
            <span className="truncate">{t.files.pasteBtn}</span>
          </button>
        </div>
      </div>
    </>
  );
}
