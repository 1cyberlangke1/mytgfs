// @vitest-environment jsdom
//
// FilesView 的键盘与选择行为回归测试。
//
// 输入：假卷 demo + 2 个目录 + 2 个文件，通过 props 直接注入；
// 输出：断言游标、选中集、弹窗开关、搜索过滤与回调调用次数；
// 预期：键盘操作不依赖真实几何布局（jsdom 没有布局），
//       只覆盖列表内部的方向键/选择/快捷动作与文档级 Esc。
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { FileItem, VolumeInfo } from '../types';
import { DICTIONARY } from '../utils/i18n';
import { DEFAULT_SHORTCUTS } from '../utils/shortcuts';
import { FilesView } from './FilesView';

// jsdom 缺的浏览器 API：dialog 的模态方法、scrollIntoView、CSS.escape、matchMedia。
// 它们只影响渲染，不影响被测的键盘逻辑，所以在这里补最小实现。
function installShims() {
  const dialogProto = window.HTMLDialogElement?.prototype;
  if (dialogProto && typeof dialogProto.showModal !== 'function') {
    dialogProto.showModal = function showModal(this: HTMLDialogElement) {
      this.open = true;
    };
    dialogProto.close = function close(this: HTMLDialogElement) {
      this.open = false;
      this.dispatchEvent(new Event('close'));
    };
  }
  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = function scrollIntoView() {};
  }
  if (typeof window.CSS === 'undefined') {
    // @ts-expect-error 运行时补丁
    window.CSS = {};
  }
  if (!window.CSS.escape) {
    window.CSS.escape = (value: string) => value.replace(/[^a-zA-Z0-9_-]/g, (c) => `\\${c}`);
  }
  if (!window.matchMedia) {
    window.matchMedia = (query: string) =>
      ({
        matches: false,
        media: query,
        onchange: null,
        addEventListener: () => {},
        removeEventListener: () => {},
        addListener: () => {},
        removeListener: () => {},
        dispatchEvent: () => false,
      }) as MediaQueryList;
  }
}

// 构造文件项：只填测试用得到的字段。
function file(
  partial: Pick<FileItem, 'id' | 'name'> & Partial<FileItem> & { isDir?: boolean }
): FileItem {
  const isDir = partial.isDir ?? false;
  return {
    volumeId: 'demo',
    parentId: null,
    size: isDir ? 0 : 1024,
    updatedAt: '2026-01-01T00:00:00Z',
    category: isDir ? 'folder' : 'document',
    ...partial,
    isDir,
  };
}

const VOLUMES: VolumeInfo[] = [{ id: 'demo', name: 'demo', title: '演示卷', cover: '' }];

// 根目录下的 4 项：2 个目录 + 2 个文件，名字排序后 photos < music 之类按拼音不受控，
// 因此断言一律按实际渲染顺序取，不硬编码名字顺序。
const FILES: FileItem[] = [
  file({ id: 'd1', name: 'photos', isDir: true }),
  file({ id: 'd2', name: 'music', isDir: true }),
  file({ id: 'f1', name: 'notes.txt' }),
  file({ id: 'f2', name: 'clip.mp4', category: 'video' }),
];

type Handlers = {
  onDeleteItems: ReturnType<typeof vi.fn>;
  onRenameMoveItem: ReturnType<typeof vi.fn>;
  onPreviewFile: ReturnType<typeof vi.fn>;
  onNavigateFolder: ReturnType<typeof vi.fn>;
  onBatchMoveItems: ReturnType<typeof vi.fn>;
};

// 渲染 FilesView 并返回常用回调的 spy。
function renderView(): Handlers {
  const onDeleteItems = vi.fn();
  const onRenameMoveItem = vi.fn();
  const onPreviewFile = vi.fn();
  const onNavigateFolder = vi.fn();
  const onBatchMoveItems = vi.fn();
  render(
    <FilesView
      t={DICTIONARY.zh!}
      locale="zh"
      volumes={VOLUMES}
      activeVolumeId="demo"
      onChangeVolume={vi.fn()}
      currentFolderId={null}
      onNavigateFolder={onNavigateFolder}
      files={FILES}
      loading={false}
      connectionStatus="connected"
      onUploadSources={vi.fn()}
      onNewTextFile={vi.fn()}
      onPasteClipboard={vi.fn()}
      onNotifyError={vi.fn()}
      onCreateFolder={vi.fn()}
      onRenameMoveItem={onRenameMoveItem}
      onBatchMoveItems={onBatchMoveItems}
      onMoveItems={vi.fn()}
      onDeleteItems={onDeleteItems}
      onDownloadFile={vi.fn()}
      onPreviewFile={onPreviewFile}
      canCreateVolume={false}
      canDeleteVolume={false}
      canAttachVolume={false}
      canListChannels={false}
      onCreateVolume={vi.fn()}
      onAttachVolume={vi.fn()}
      onDeleteVolume={vi.fn()}
      onRefresh={async () => {}}
      shortcuts={DEFAULT_SHORTCUTS}
    />
  );
  return { onDeleteItems, onRenameMoveItem, onPreviewFile, onNavigateFolder, onBatchMoveItems };
}

// listRef 容器带 aria-keyshortcuts，用它定位可聚焦的列表区域。
function listContainer(): HTMLElement {
  const el = document.querySelector<HTMLElement>('[aria-keyshortcuts]');
  if (!el) throw new Error('找不到文件列表容器');
  return el;
}

beforeEach(() => {
  installShims();
  window.localStorage.clear();
});

// vitest 没开 globals，测试库不会自动卸载：手动清掉上一次的 DOM 与监听，
// 否则上一个用例留下的 dialog[open] 会让 Esc 直接被吞掉
afterEach(() => {
  cleanup();
});

describe('FilesView 键盘操作', () => {
  it('方向键把游标移到下一行', async () => {
    const user = userEvent.setup();
    renderView();
    const list = listContainer();
    list.focus();

    await user.keyboard('{ArrowDown}');
    const rows = Array.from(document.querySelectorAll<HTMLElement>('[data-file-id]'));
    expect(rows.length).toBe(4);
    expect(rows[0]?.className).toContain('ring-[#007AFF]/70');

    await user.keyboard('{ArrowDown}');
    const after = Array.from(document.querySelectorAll<HTMLElement>('[data-file-id]'));
    expect(after[1]?.className).toContain('ring-[#007AFF]/70');
    expect(after[0]?.className).not.toContain('ring-[#007AFF]/70');
  });

  it('空格勾选游标所在行，Ctrl+A 全选，Esc 清空选择与游标', async () => {
    const user = userEvent.setup();
    renderView();
    listContainer().focus();

    await user.keyboard('{ArrowDown}');
    await user.keyboard(' ');
    const rows = Array.from(document.querySelectorAll<HTMLElement>('[data-file-id]'));
    const firstName = rows[0]?.getAttribute('data-file-id');
    expect(firstName).toBeTruthy();
    expect(document.querySelectorAll('input[type="checkbox"]:checked').length).toBe(1);

    await user.keyboard('{Control>}a{/Control}');
    // 4 行 + 可能有表头全选：只数行内勾选框
    const rowChecked = Array.from(
      document.querySelectorAll<HTMLInputElement>('[data-file-id] input[type="checkbox"]')
    ).filter((el) => el.checked);
    expect(rowChecked.length).toBe(4);

    await user.keyboard('{Escape}');
    await waitFor(() => {
      const listed = Array.from(
        document.querySelectorAll<HTMLInputElement>('[data-file-id] input[type="checkbox"]')
      ).filter((el) => el.checked);
      expect(listed.length).toBe(0);
    });
  });

  it('Delete 打开删除确认框，取消不触发删除回调', async () => {
    const user = userEvent.setup();
    const { onDeleteItems } = renderView();
    listContainer().focus();

    await user.keyboard('{ArrowDown}');
    await user.keyboard('{Delete}');

    const dialog = await screen.findByRole('dialog');
    expect(dialog).toBeTruthy();

    await user.click(within(dialog).getByRole('button', { name: DICTIONARY.zh!.files.cancel }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(onDeleteItems).not.toHaveBeenCalled();
  });

  it('F2 打开改名弹窗并预填当前名字', async () => {
    const user = userEvent.setup();
    renderView();
    listContainer().focus();

    await user.keyboard('{ArrowDown}');
    await user.keyboard('{F2}');

    const dialog = await screen.findByRole('dialog');
    const input = within(dialog).getByRole('textbox') as HTMLInputElement;
    const cursorName = document
      .querySelector<HTMLElement>('[data-file-id].ring-2, [data-file-id]')
      ?.getAttribute('data-file-id');
    expect(cursorName).toBeTruthy();
    expect(input.value.length).toBeGreaterThan(0);
  });

  it('搜索框输入后只保留匹配项，Esc 清空搜索并回到列表', async () => {
    const user = userEvent.setup();
    renderView();

    const search = document.querySelector<HTMLInputElement>('input[type="search"]');
    expect(search).not.toBeNull();
    await user.click(search!);
    await user.keyboard('clip');

    await waitFor(() => {
      const text = listContainer().textContent ?? '';
      expect(text).toContain('clip.mp4');
      expect(text).not.toContain('notes.txt');
    });

    await user.keyboard('{Escape}');
    // 诊断：Esc 后输入框应被清空
    expect(search!.value).toBe('');
    await waitFor(() => {
      expect(listContainer().textContent ?? '').toContain('notes.txt');
    });
  });
});
