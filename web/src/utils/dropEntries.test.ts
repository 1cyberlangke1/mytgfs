import { describe, expect, it } from 'vitest';
import { collectDroppedFiles } from './dropEntries';

// fileEntry 造一个假的文件 entry，内容用真实字节数占位。
function fileEntry(name: string, size = 1): FileSystemFileEntry {
  const file = new File([new Uint8Array(size)], name);
  return {
    isFile: true,
    isDirectory: false,
    name,
    file: (ok: (f: File) => void) => ok(file),
  } as unknown as FileSystemFileEntry;
}

// dirEntry 造一个按批次返回子项的目录 entry：最后一批后必须返回空数组。
function dirEntry(name: string, batches: FileSystemEntry[][]): FileSystemDirectoryEntry {
  let index = 0;
  return {
    isFile: false,
    isDirectory: true,
    name,
    createReader: () =>
      ({
        readEntries: (ok: (batch: FileSystemEntry[]) => void) => {
          const batch = batches[index] ?? [];
          index += 1;
          ok(batch);
        },
      }) as unknown as FileSystemDirectoryReader,
  } as unknown as FileSystemDirectoryEntry;
}

// dropOf 造一个只带 items/files 的 DataTransfer 替身。
function dropOf(items: Array<{ entry?: FileSystemEntry | null; file?: File }>): DataTransfer {
  return {
    items: items.map(({ entry, file }) => ({
      kind: 'file',
      webkitGetAsEntry: entry === undefined ? undefined : () => entry ?? null,
      getAsFile: () => file ?? null,
    })),
    files: items.map(({ file }) => file).filter(Boolean),
  } as unknown as DataTransfer;
}

describe('collectDroppedFiles', () => {
  it('没有 item 时回落到 files 平面列表', async () => {
    const dt = {
      items: [],
      files: [new File([new Uint8Array(1)], 'a.txt'), new File([new Uint8Array(2)], 'b.txt')],
    } as unknown as DataTransfer;
    const out = await collectDroppedFiles(dt);
    expect(out.map((s) => [s.file.name, s.rel])).toEqual([
      ['a.txt', ''],
      ['b.txt', ''],
    ]);
  });

  it('顶层单个文件 rel 为空', async () => {
    const out = await collectDroppedFiles(dropOf([{ entry: fileEntry('a.txt') }]));
    expect(out).toHaveLength(1);
    expect(out[0]?.rel).toBe('');
  });

  it('拖文件夹时递归展开并保留相对路径', async () => {
    const top = dirEntry('top', [
      [fileEntry('a.txt'), dirEntry('sub', [[fileEntry('b.txt')]])],
    ]);
    const out = await collectDroppedFiles(dropOf([{ entry: top }]));
    expect(out.map((s) => s.rel).sort()).toEqual(['top/a.txt', 'top/sub/b.txt']);
  });

  it('目录分批返回时不会丢掉后面的文件', async () => {
    const many = dirEntry('many', [
      [fileEntry('1.txt'), fileEntry('2.txt')],
      [fileEntry('3.txt')],
      [],
    ]);
    const out = await collectDroppedFiles(dropOf([{ entry: many }]));
    expect(out.map((s) => s.file.name)).toEqual(['1.txt', '2.txt', '3.txt']);
  });

  it('浏览器不给 entry 时回落到 getAsFile', async () => {
    const out = await collectDroppedFiles(
      dropOf([{ entry: null, file: new File([new Uint8Array(3)], 'c.txt') }])
    );
    expect(out.map((s) => [s.file.name, s.rel])).toEqual([['c.txt', '']]);
  });
});
