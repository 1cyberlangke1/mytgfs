// @vitest-environment jsdom
//
// 同名冲突弹窗的行为回归。
//
// 输入：两个与目录已有文件重名的草稿项；
// 输出：断言默认动作、全部按钮、确认交回的选择、取消回调；
// 预期：默认不覆盖任何文件，全部跳过时不允许确认。
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { DICTIONARY } from '../utils/i18n';
import { stageSources } from '../utils/uploadPlan';
import { ConflictDialog } from './ConflictDialog';

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

// setup 造两个草稿项与其冲突描述。
function setup() {
  const items = stageSources(
    [
      { file: new File(['a'], 'a.txt'), rel: '' },
      { file: new File(['b'], 'b.txt'), rel: 'dir/b.txt' },
    ],
    'vol',
    null
  );
  const conflicts = [
    { id: items[0]!.id, name: 'a.txt', suggested: 'a (2).txt' },
    { id: items[1]!.id, name: 'b.txt', suggested: 'dir/b (2).txt' },
  ];
  return { items, conflicts };
}

describe('ConflictDialog', () => {
  beforeEach(() => installDialogShim());
  afterEach(() => cleanup());

  it('默认改名：显示建议名，确认交回 rename', async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn();
    const { items, conflicts } = setup();
    render(
      <ConflictDialog
        t={t}
        items={items}
        conflicts={conflicts}
        onConfirm={onConfirm}
        onCancel={vi.fn()}
      />
    );

    const dialog = screen.getByRole('dialog');
    expect(within(dialog).getByText('将保存为 a (2).txt')).toBeTruthy();
    await user.click(within(dialog).getByRole('button', { name: t.files.conflictConfirm }));

    const choices = onConfirm.mock.calls[0]?.[0] as Record<string, string>;
    expect(choices[conflicts[0]!.id]).toBe('rename');
    expect(choices[conflicts[1]!.id]).toBe('rename');
  });

  it('全部覆盖后每个下拉都变成覆盖', async () => {
    const user = userEvent.setup();
    const { items, conflicts } = setup();
    render(
      <ConflictDialog
        t={t}
        items={items}
        conflicts={conflicts}
        onConfirm={vi.fn()}
        onCancel={vi.fn()}
      />
    );

    await user.click(screen.getByRole('button', { name: t.files.conflictAllOverwrite }));
    const selects = screen.getAllByRole('combobox') as HTMLSelectElement[];
    expect(selects.map((s) => s.value)).toEqual(['overwrite', 'overwrite']);
    expect(screen.getAllByText(t.files.conflictOverwriteHint)).toHaveLength(2);
  });

  it('全部跳过时确认被禁用', async () => {
    const user = userEvent.setup();
    const { items, conflicts } = setup();
    render(
      <ConflictDialog
        t={t}
        items={items}
        conflicts={conflicts}
        onConfirm={vi.fn()}
        onCancel={vi.fn()}
      />
    );

    await user.click(screen.getByRole('button', { name: t.files.conflictAllSkip }));
    const confirm = screen.getByRole('button', {
      name: t.files.conflictConfirm,
    }) as HTMLButtonElement;
    expect(confirm.disabled).toBe(true);
  });

  it('单个下拉改选择只影响该行', async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn();
    const { items, conflicts } = setup();
    render(
      <ConflictDialog
        t={t}
        items={items}
        conflicts={conflicts}
        onConfirm={onConfirm}
        onCancel={vi.fn()}
      />
    );

    await user.selectOptions(
      screen.getByLabelText(`${t.files.conflictRowLabel}: a.txt`),
      'skip'
    );
    await user.click(screen.getByRole('button', { name: t.files.conflictConfirm }));
    const choices = onConfirm.mock.calls[0]?.[0] as Record<string, string>;
    expect(choices[conflicts[0]!.id]).toBe('skip');
    expect(choices[conflicts[1]!.id]).toBe('rename');
  });

  it('取消走 onCancel', async () => {
    const user = userEvent.setup();
    const onCancel = vi.fn();
    const { items, conflicts } = setup();
    render(
      <ConflictDialog
        t={t}
        items={items}
        conflicts={conflicts}
        onConfirm={vi.fn()}
        onCancel={onCancel}
      />
    );
    await user.click(screen.getByRole('button', { name: t.files.cancel }));
    expect(onCancel).toHaveBeenCalledTimes(1);
  });
});
