// @vitest-environment jsdom
//
// FilePreviewModal 单元与行为回归测试。
//
// 输入：文件条目与平台能力注入；
// 输出：断言内联预览上限判定逻辑，以及弹窗底部固定操作栏的多端适配；
// 预期：视频/音频永远走 Range 不受 32MB 约束，弹窗底部操作栏固定吸底，避免长内容挤出视口。

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import type { FileItem } from '../types';
import { DICTIONARY } from '../utils/i18n';
import { FilePreviewModal, inlinePreviewBlocked } from './FilePreviewModal';

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

function dummyFile(
  partial: Pick<FileItem, 'id' | 'name'> & Partial<FileItem> & { isDir?: boolean }
): FileItem {
  const isDir = partial.isDir ?? false;
  return {
    volumeId: 'demo',
    parentId: null,
    size: isDir ? 0 : 2048,
    updatedAt: '2026-01-01T00:00:00Z',
    category: isDir ? 'folder' : 'document',
    ...partial,
    isDir,
  };
}

describe('inlinePreviewBlocked', () => {
  const MB = 1024 * 1024;

  it('大视频不拦：走 Range 窗口', () => {
    expect(inlinePreviewBlocked(200 * MB, 'video', false, false)).toBe(false);
  });

  it('大音频不拦', () => {
    expect(inlinePreviewBlocked(200 * MB, 'audio', false, false)).toBe(false);
  });

  it('大 PDF 要拦：整份读取会吃满内存', () => {
    expect(inlinePreviewBlocked(33 * MB, 'pdf', false, false)).toBe(true);
  });

  it('大文本要拦', () => {
    expect(inlinePreviewBlocked(40 * MB, 'document', false, false)).toBe(true);
  });

  it('小文件不拦', () => {
    expect(inlinePreviewBlocked(1 * MB, 'pdf', false, false)).toBe(false);
  });

  it('本地流覆盖不拦：下载页预览走 /api/downloads/file', () => {
    expect(inlinePreviewBlocked(200 * MB, 'pdf', false, true)).toBe(false);
  });

  it('目录不拦', () => {
    expect(inlinePreviewBlocked(0, 'folder', true, false)).toBe(false);
  });
});

describe('FilePreviewModal 底部固定操作栏', () => {
  beforeEach(() => {
    installDialogShim();
  });

  afterEach(() => {
    cleanup();
  });

  it('渲染固定底栏且包含关闭与下载按钮', () => {
    const file = dummyFile({ id: 'f1', name: 'sample.txt' });
    const onDownload = vi.fn();
    const onClose = vi.fn();

    render(
      <FilePreviewModal
        file={file}
        allFiles={[file]}
        t={DICTIONARY.zh!}
        locale="zh"
        onClose={onClose}
        onDownload={onDownload}
        canDownload
      />
    );

    const downloadBtn = screen.getByRole('button', {
      name: DICTIONARY.zh!.files.actionDownload,
    });
    expect(downloadBtn).toBeTruthy();

    const closeBtn = screen.getByRole('button', {
      name: DICTIONARY.zh!.files.closeDetail,
    });
    expect(closeBtn).toBeTruthy();

    // 底部操作栏具有 shrink-0 与 border-t，保证吸底固定
    const footer = downloadBtn.closest('div');
    expect(footer?.className).toContain('shrink-0');
    expect(footer?.className).toContain('border-t');

    fireEvent.click(downloadBtn);
    expect(onDownload).toHaveBeenCalledWith(file);

    fireEvent.click(closeBtn);
    expect(onClose).toHaveBeenCalled();
  });

  it('支持自定义操作文案（例如下载页的「打开」）', () => {
    const file = dummyFile({ id: 'f2', name: 'song.mp3', category: 'audio' });
    const onDownload = vi.fn();

    render(
      <FilePreviewModal
        file={file}
        allFiles={[file]}
        t={DICTIONARY.zh!}
        locale="zh"
        onClose={vi.fn()}
        onDownload={onDownload}
        canDownload
        actionLabel="系统打开"
      />
    );

    const actionBtn = screen.getByRole('button', { name: '系统打开' });
    expect(actionBtn).toBeTruthy();
  });

  it('当 canDownload 为 false 时隐藏下载按钮但保留关闭按钮', () => {
    const file = dummyFile({ id: 'f3', name: 'image.png', category: 'image' });

    render(
      <FilePreviewModal
        file={file}
        allFiles={[file]}
        t={DICTIONARY.zh!}
        locale="zh"
        onClose={vi.fn()}
        onDownload={vi.fn()}
        canDownload={false}
      />
    );

    expect(
      screen.queryByRole('button', { name: DICTIONARY.zh!.files.actionDownload })
    ).toBeNull();
    expect(
      screen.getByRole('button', { name: DICTIONARY.zh!.files.closeDetail })
    ).toBeTruthy();
  });
});
