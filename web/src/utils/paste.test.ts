// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import { extractPasteContent, isEditableTarget, pastedFileName, readClipboardContent } from './paste';

// clipboardOf 造一个带 items/text 的 clipdata 替身。
function clipboardOf(
  items: Array<{ kind: string; file?: File }>,
  text = ''
): DataTransfer {
  return {
    items: items.map(({ kind, file }) => ({ kind, getAsFile: () => file ?? null })),
    getData: () => text,
  } as unknown as DataTransfer;
}

describe('pastedFileName', () => {
  it('真实文件名原样保留', () => {
    const file = new File([new Uint8Array(1)], 'report.pdf');
    expect(pastedFileName(file)).toBe('report.pdf');
  });

  it('浏览器的通用图片名改成带时间戳的唯一名', () => {
    const file = new File([new Uint8Array(1)], 'image.png', { type: 'image/png' });
    expect(pastedFileName(file, Date.UTC(2026, 10, 16, 10, 30, 12))).toMatch(
      /^pasted-\d{8}-\d{6}\.png$/
    );
  });

  it('名字为空时按 MIME 猜扩展名', () => {
    const file = new File([new Uint8Array(1)], '', { type: 'image/jpeg' });
    expect(pastedFileName(file, Date.UTC(2026, 10, 16, 10, 30, 12))).toMatch(/\.jpg$/);
  });
});

describe('extractPasteContent', () => {
  it('纯文本返回 text', () => {
    const out = extractPasteContent(clipboardOf([], 'hello'));
    expect(out).toEqual({ files: [], text: 'hello' });
  });

  it('文件优先，且不再带文字', () => {
    const file = new File([new Uint8Array(4)], 'shot.png', { type: 'image/png' });
    const out = extractPasteContent(clipboardOf([{ kind: 'file', file }], 'ignored'));
    expect(out.text).toBe('');
    expect(out.files.map((s) => [s.file.name, s.rel])).toEqual([['shot.png', '']]);
  });

  it('忽略非文件条目，没有内容时返回空', () => {
    const out = extractPasteContent(clipboardOf([{ kind: 'string' }], ''));
    expect(out).toEqual({ files: [], text: '' });
  });
});

describe('readClipboardContent', () => {
  // mockClipboard 覆写 navigator.clipboard.read，返回固定条目。
  function mockClipboard(
    entries: Array<{ types: string[]; blobs?: Record<string, Blob> }>
  ): void {
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: {
        read: async () =>
          entries.map((entry) => ({
            types: entry.types,
            getType: async (type: string) => entry.blobs?.[type] ?? new Blob([]),
          })),
      },
    });
  }

  it('图片落成 File，名字带时间戳', async () => {
    const png = new Blob([new Uint8Array(3)], { type: 'image/png' });
    mockClipboard([{ types: ['image/png'], blobs: { 'image/png': png } }]);
    const out = await readClipboardContent(Date.UTC(2026, 10, 16, 10, 30, 12));
    expect(out.blockedFileList).toBe(false);
    expect(out.text).toBe('');
    expect(out.files).toHaveLength(1);
    expect(out.files[0]!.file.name).toMatch(/^pasted-\d{8}-\d{6}\.png$/);
    expect(out.files[0]!.file.type).toBe('image/png');
  });

  it('多张同名图片按序号区分', async () => {
    const png = new Blob([new Uint8Array(3)], { type: 'image/png' });
    mockClipboard([
      { types: ['image/png'], blobs: { 'image/png': png } },
      { types: ['image/png'], blobs: { 'image/png': png } },
    ]);
    const out = await readClipboardContent(Date.UTC(2026, 10, 16, 10, 30, 12));
    expect(out.files.map((s) => s.file.name)).toEqual([
      expect.stringMatching(/^pasted-\d{8}-\d{6}\.png$/),
      expect.stringMatching(/^pasted-\d{8}-\d{6}-2\.png$/),
    ]);
  });

  it('非图片二进制按 MIME 取扩展名', async () => {
    const pdf = new Blob([new Uint8Array(5)], { type: 'application/pdf' });
    mockClipboard([{ types: ['application/pdf'], blobs: { 'application/pdf': pdf } }]);
    const out = await readClipboardContent(Date.UTC(2026, 10, 16, 10, 30, 12));
    expect(out.files[0]!.file.name).toMatch(/\.pdf$/);
  });

  it('纯文本返回 text', async () => {
    mockClipboard([{ types: ['text/plain'], blobs: { 'text/plain': new Blob(['hi']) } }]);
    const out = await readClipboardContent();
    expect(out).toEqual({ files: [], text: 'hi', blockedFileList: false });
  });

  it('文件路径列表标记 blockedFileList', async () => {
    mockClipboard([{ types: ['text/uri-list'] }]);
    const out = await readClipboardContent();
    expect(out).toEqual({ files: [], text: '', blockedFileList: true });
  });

  it('没有异步剪贴板 API 时抛错', async () => {
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined });
    await expect(readClipboardContent()).rejects.toThrow('clipboard_unavailable');
  });
});

describe('isEditableTarget', () => {
  it('输入控件与 contenteditable 放行', () => {
    expect(isEditableTarget(document.createElement('input'))).toBe(true);
    expect(isEditableTarget(document.createElement('textarea'))).toBe(true);
    const div = document.createElement('div');
    Object.defineProperty(div, 'isContentEditable', { value: true });
    expect(isEditableTarget(div)).toBe(true);
  });

  it('普通元素与 null 不拦截', () => {
    expect(isEditableTarget(document.createElement('div'))).toBe(false);
    expect(isEditableTarget(null)).toBe(false);
  });
});
