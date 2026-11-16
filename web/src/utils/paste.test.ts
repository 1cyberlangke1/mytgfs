// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import { extractPasteContent, isEditableTarget, pastedFileName } from './paste';

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
