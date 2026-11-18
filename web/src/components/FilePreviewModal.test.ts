// inlinePreviewBlocked 单测：视频/音频走 Range 窗口，不该被 32 MiB 上限拦住；
// 图片/文本/PDF 仍按整份读取，超过上限只能下载。

import { describe, expect, it } from 'vitest';
import { inlinePreviewBlocked } from './FilePreviewModal';

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
