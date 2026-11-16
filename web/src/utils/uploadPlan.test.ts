import { describe, expect, it } from 'vitest';
import {
  keptStaged,
  sourcesFromFiles,
  stageSources,
  stagedDisplayPath,
  stagedTotalBytes,
  toTransferTasks,
} from './uploadPlan';

// fileOf 造一个指定大小与名字的 File；size 用真实字节数，避免 mock 出不可能的体积。
function fileOf(name: string, size = 1): File {
  return new File([new Uint8Array(size)], name);
}

describe('sourcesFromFiles', () => {
  it('普通选择不带相对路径', () => {
    const out = sourcesFromFiles([fileOf('a.txt'), fileOf('b.txt')]);
    expect(out.map((s) => [s.file.name, s.rel])).toEqual([
      ['a.txt', ''],
      ['b.txt', ''],
    ]);
  });

  it('目录选择保留 webkitRelativePath 层级', () => {
    const deep = fileOf('c.txt');
    Object.defineProperty(deep, 'webkitRelativePath', { value: 'top/sub/c.txt' });
    const out = sourcesFromFiles([deep]);
    expect(out[0]?.rel).toBe('top/sub/c.txt');
  });
});

describe('stageSources', () => {
  it('每项拿到唯一 id 并记录目标位置', () => {
    const staged = stageSources(
      [
        { file: fileOf('a.txt', 10), rel: '' },
        { file: fileOf('b.txt', 20), rel: 'dir/b.txt' },
      ],
      'vol',
      'folder-1'
    );
    expect(staged).toHaveLength(2);
    expect(new Set(staged.map((s) => s.id)).size).toBe(2);
    expect(staged.every((s) => s.volumeId === 'vol' && s.parentId === 'folder-1')).toBe(true);
    expect(staged[1]?.rel).toBe('dir/b.txt');
  });
});

describe('stagedTotalBytes', () => {
  it('空列表为 0，否则为字节和', () => {
    expect(stagedTotalBytes([])).toBe(0);
    const staged = stageSources(
      [
        { file: fileOf('a.txt', 10), rel: '' },
        { file: fileOf('b.txt', 32), rel: '' },
      ],
      'vol',
      null
    );
    expect(stagedTotalBytes(staged)).toBe(42);
  });
});

describe('keptStaged', () => {
  it('剔除被移除项并保持原顺序', () => {
    const staged = stageSources(
      [
        { file: fileOf('a.txt'), rel: '' },
        { file: fileOf('b.txt'), rel: '' },
        { file: fileOf('c.txt'), rel: '' },
      ],
      'vol',
      null
    );
    const kept = keptStaged(staged, new Set([staged[1]!.id]));
    expect(kept.map((s) => s.file.name)).toEqual(['a.txt', 'c.txt']);
  });

  it('全部移除时返回空数组', () => {
    const staged = stageSources([{ file: fileOf('a.txt'), rel: '' }], 'vol', null);
    expect(keptStaged(staged, new Set(staged.map((s) => s.id)))).toEqual([]);
  });
});

describe('stagedDisplayPath', () => {
  it('有相对路径就用相对路径，否则用文件名', () => {
    const staged = stageSources(
      [
        { file: fileOf('a.txt'), rel: '' },
        { file: fileOf('b.txt'), rel: 'dir/b.txt' },
      ],
      'vol',
      null
    );
    expect(stagedDisplayPath(staged[0]!)).toBe('a.txt');
    expect(stagedDisplayPath(staged[1]!)).toBe('dir/b.txt');
  });
});

describe('toTransferTasks', () => {
  it('生成 queued 任务并带上批次号与目标目录', () => {
    const staged = stageSources(
      [{ file: fileOf('clip.mp4', 64), rel: 'dir/clip.mp4' }],
      'vol',
      'parent-1'
    );
    const tasks = toTransferTasks(staged, 'batch-9');
    expect(tasks).toHaveLength(1);
    const task = tasks[0]!;
    expect(task).toMatchObject({
      fileName: 'clip.mp4',
      volumeId: 'vol',
      targetParentId: 'parent-1',
      direction: 'upload',
      size: 64,
      transferredBytes: 0,
      state: 'queued',
      opKind: 'upload',
      batchId: 'batch-9',
      category: 'video',
    });
    expect(task.id.startsWith('task-')).toBe(true);
  });

  it('多个文件的任务 id 互不相同', () => {
    const staged = stageSources(
      [
        { file: fileOf('a.txt'), rel: '' },
        { file: fileOf('b.txt'), rel: '' },
      ],
      'vol',
      null
    );
    const tasks = toTransferTasks(staged, 'batch-1');
    expect(new Set(tasks.map((t) => t.id)).size).toBe(2);
  });
});
