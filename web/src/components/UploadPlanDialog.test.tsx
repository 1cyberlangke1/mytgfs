// @vitest-environment jsdom
//
// 上传前清单的行为回归测试。
//
// 输入：两个草稿文件，通过 props 注入；
// 输出：断言移除、全部移除、恢复全选与确认时交出的清单；
// 预期：被移除的文件不会出现在 onConfirm 的参数里。
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { DICTIONARY } from '../utils/i18n';
import { stageSources } from '../utils/uploadPlan';
import { UploadPlanDialog } from './UploadPlanDialog';

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

// stagedOf 造两个带相对路径的草稿项。
function stagedOf() {
  return stageSources(
    [
      { file: new File([new Uint8Array(10)], 'a.txt'), rel: '' },
      { file: new File([new Uint8Array(32)], 'b.txt'), rel: 'dir/b.txt' },
    ],
    'vol',
    null
  );
}

const t = DICTIONARY.zh!;

describe('UploadPlanDialog', () => {
  beforeEach(() => {
    installDialogShim();
  });
  afterEach(() => cleanup());

  it('列出全部文件并给出合计', () => {
    render(<UploadPlanDialog t={t} locale="zh" items={stagedOf()} onConfirm={vi.fn()} onCancel={vi.fn()} />);
    const dialog = screen.getByRole('dialog');
    expect(within(dialog).getByText('a.txt')).toBeTruthy();
    expect(within(dialog).getByText('dir/b.txt')).toBeTruthy();
    expect(within(dialog).getByText(/2 个文件/)).toBeTruthy();
  });

  it('移除单个文件后只剩另一个，确认时不再带上被移除的', async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn();
    const items = stagedOf();
    render(<UploadPlanDialog t={t} locale="zh" items={items} onConfirm={onConfirm} onCancel={vi.fn()} />);
    const dialog = screen.getByRole('dialog');

    await user.click(
      within(dialog).getByRole('button', { name: `${t.files.uploadPlanRemove}: a.txt` })
    );
    expect(within(dialog).queryByText('a.txt')).toBeNull();

    await user.click(within(dialog).getByRole('button', { name: t.files.uploadPlanConfirm }));
    expect(onConfirm).toHaveBeenCalledTimes(1);
    expect((onConfirm.mock.calls[0]?.[0] as { rel: string }[]).map((s) => s.rel)).toEqual([
      'dir/b.txt',
    ]);
  });

  it('全部移除后确认被禁用，恢复全选后回来', async () => {
    const user = userEvent.setup();
    render(<UploadPlanDialog t={t} locale="zh" items={stagedOf()} onConfirm={vi.fn()} onCancel={vi.fn()} />);
    const dialog = screen.getByRole('dialog');

    await user.click(within(dialog).getByRole('button', { name: t.files.uploadPlanRemoveAll }));
    expect(within(dialog).getByText(t.files.uploadPlanEmpty)).toBeTruthy();
    const confirm = within(dialog).getByRole('button', { name: t.files.uploadPlanConfirm });
    expect((confirm as HTMLButtonElement).disabled).toBe(true);

    await user.click(within(dialog).getByRole('button', { name: t.files.uploadPlanRestoreAll }));
    expect(within(dialog).getByText('a.txt')).toBeTruthy();
  });

  it('取消走 onCancel', async () => {
    const user = userEvent.setup();
    const onCancel = vi.fn();
    render(<UploadPlanDialog t={t} locale="zh" items={stagedOf()} onConfirm={vi.fn()} onCancel={onCancel} />);
    await user.click(screen.getByRole('button', { name: t.files.cancel }));
    expect(onCancel).toHaveBeenCalledTimes(1);
  });
});
