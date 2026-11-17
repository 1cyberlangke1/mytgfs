// @vitest-environment jsdom
//
// 统一上传入口的行为回归测试。
//
// 输入：四个入口回调（文件 / 文件夹 / 新建文本 / 粘贴剪贴板）；
// 输出：断言触发按钮的 aria 状态与菜单项点击后的回调；
// 预期：所有新建内容的入口都收在一个按钮下，点击任一项只触发对应回调。
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { DICTIONARY } from '../utils/i18n';
import { UploadMenu } from './UploadMenu';

// jsdom 没有实现 popover 的开关方法：补最小实现，
// toggle 事件必须带 newState，组件的 aria-expanded 依赖它。
function installPopoverShim() {
  const proto = HTMLElement.prototype as HTMLElement & {
    showPopover?: () => void;
    hidePopover?: () => void;
  };
  if (typeof proto.showPopover === 'function') return;
  const dispatch = (el: HTMLElement, newState: string) => {
    const event = new Event('toggle');
    Object.defineProperty(event, 'newState', { value: newState });
    el.dispatchEvent(event);
  };
  proto.showPopover = function showPopover(this: HTMLElement) {
    dispatch(this, 'open');
  };
  proto.hidePopover = function hidePopover(this: HTMLElement) {
    dispatch(this, 'closed');
  };
}

const t = DICTIONARY.zh!;

// propsOf 生成四个回调，便于逐个断言。
function propsOf() {
  return {
    onPickFile: vi.fn(),
    onPickFolder: vi.fn(),
    onNewText: vi.fn(),
    onPasteClipboard: vi.fn(),
    canPaste: true,
  };
}

describe('UploadMenu', () => {
  beforeEach(() => installPopoverShim());
  afterEach(() => cleanup());

  it('渲染一个触发按钮与四个菜单项', () => {
    const props = propsOf();
    render(<UploadMenu t={t} {...props} />);
    const trigger = screen.getByRole('button', { name: t.files.uploadMenuLabel });
    expect(trigger.getAttribute('aria-haspopup')).toBe('menu');
    expect(trigger.getAttribute('aria-expanded')).toBe('false');
    for (const label of [
      t.files.uploadFileBtn,
      t.files.uploadDirBtn,
      t.files.newTextBtn,
      t.files.pasteBtn,
    ]) {
      expect(screen.getByText(label)).toBeTruthy();
    }
    // 单文件上限在选择前就要可见，并挂到「上传文件」项上供读屏朗读
    const hint = screen.getByText(t.files.uploadMaxHint);
    const fileItem = screen.getByText(t.files.uploadFileBtn).closest('button');
    expect(fileItem?.getAttribute('aria-describedby')).toBe(hint.id);
  });

  it('点击触发按钮后 aria-expanded 变 true', async () => {
    const user = userEvent.setup();
    render(<UploadMenu t={t} {...propsOf()} />);
    const trigger = screen.getByRole('button', { name: t.files.uploadMenuLabel });
    await user.click(trigger);
    expect(trigger.getAttribute('aria-expanded')).toBe('true');
  });

  it('每个菜单项只触发自己的回调', async () => {
    const user = userEvent.setup();
    const props = propsOf();
    render(<UploadMenu t={t} {...props} />);

    await user.click(screen.getByText(t.files.uploadFileBtn));
    expect(props.onPickFile).toHaveBeenCalledTimes(1);
    expect(props.onPickFolder).not.toHaveBeenCalled();

    await user.click(screen.getByText(t.files.uploadDirBtn));
    expect(props.onPickFolder).toHaveBeenCalledTimes(1);

    await user.click(screen.getByText(t.files.newTextBtn));
    expect(props.onNewText).toHaveBeenCalledTimes(1);

    await user.click(screen.getByText(t.files.pasteBtn));
    expect(props.onPasteClipboard).toHaveBeenCalledTimes(1);
    expect(props.onPickFile).toHaveBeenCalledTimes(1);
  });

  it('选中菜单项后收起菜单', async () => {
    const user = userEvent.setup();
    render(<UploadMenu t={t} {...propsOf()} />);
    const trigger = screen.getByRole('button', { name: t.files.uploadMenuLabel });
    await user.click(trigger);
    await user.click(screen.getByText(t.files.newTextBtn));
    expect(trigger.getAttribute('aria-expanded')).toBe('false');
  });
});
