// @vitest-environment jsdom
//
// 新建文本弹窗的行为回归测试。
//
// 输入：空正文 / 非法文件名 / 预填正文三种情形；
// 输出：断言错误提示与 onSave 的调用参数；
// 预期：后端拒绝 size=0，所以空正文必须在提交前挡住。
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { DICTIONARY } from '../utils/i18n';
import { TextFileDialog, validateTextDraft } from './TextFileDialog';

// jsdom 没有实现 dialog 的模态方法，补最小实现让 Modal 能挂载。
function installDialogShim() {
  const proto = window.HTMLDialogElement?.prototype;
  if (proto && typeof proto.showModal !== 'function') {
    proto.showModal = function showModal(this: HTMLDialogElement) {
      this.open = true;
    };
    proto.close = function close(this: HTMLDialogElement) {
      this.open = false;
      this.dispatchEvent(new Event('close'));
    };
  }
}

const t = DICTIONARY.zh!;

describe('validateTextDraft', () => {
  it('空正文、非法文件名被拒；正常内容通过', () => {
    expect(validateTextDraft('a.txt', '')).toBe('body_empty');
    expect(validateTextDraft('  ', 'x')).toBe('name_invalid');
    expect(validateTextDraft('a/b.txt', 'x')).toBe('name_invalid');
    expect(validateTextDraft('a\\b.txt', 'x')).toBe('name_invalid');
    expect(validateTextDraft('a.txt', 'x')).toBeNull();
  });
});

describe('TextFileDialog', () => {
  afterEach(() => cleanup());

  it('空正文提交给出提示且不回调', async () => {
    installDialogShim();
    const user = userEvent.setup();
    const onSave = vi.fn();
    render(<TextFileDialog t={t} onSave={onSave} onClose={vi.fn()} />);
    await user.click(screen.getByRole('button', { name: t.files.textDialogSave }));
    expect(screen.getByRole('alert').textContent).toBe(t.files.textDialogBodyEmpty);
    expect(onSave).not.toHaveBeenCalled();
  });

  it('填好正文后回调文件名与正文', async () => {
    installDialogShim();
    const user = userEvent.setup();
    const onSave = vi.fn();
    render(<TextFileDialog t={t} onSave={onSave} onClose={vi.fn()} />);
    await user.type(screen.getByRole('textbox', { name: t.files.textDialogBodyLabel }), 'hello 喵');
    await user.click(screen.getByRole('button', { name: t.files.textDialogSave }));
    expect(onSave).toHaveBeenCalledWith('untitled.txt', 'hello 喵');
  });

  it('预填正文来自剪贴板', () => {
    installDialogShim();
    render(<TextFileDialog t={t} initialText="pasted" onSave={vi.fn()} onClose={vi.fn()} />);
    const body = screen.getByRole('textbox', {
      name: t.files.textDialogBodyLabel,
    }) as HTMLTextAreaElement;
    expect(body.value).toBe('pasted');
  });

  it('文本域采用项目 custom-scrollbar 样式且自适应', () => {
    installDialogShim();
    render(<TextFileDialog t={t} onSave={vi.fn()} onClose={vi.fn()} />);
    const textarea = screen.getByRole('textbox', { name: t.files.textDialogBodyLabel });
    expect(textarea.classList.contains('custom-scrollbar')).toBe(true);
  });
});
