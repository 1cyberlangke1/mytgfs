import { describe, expect, it } from 'vitest';
import {
  keptStaged,
  relDirPrefix,
  sourcesFromFiles,
  stageSources,
  stagedDisplayPath,
  stagedTotalBytes,
  toTransferTasks,
  uploadArgs,
  validateUploadName,
  withName,
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
    // 生效文件名默认就是 File 自带的名字
    expect(staged.map((s) => s.name)).toEqual(['a.txt', 'b.txt']);
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

describe('relDirPrefix', () => {
  it('取目录前缀并保留结尾斜杠', () => {
    expect(relDirPrefix('dir/sub/a.txt')).toBe('dir/sub/');
    expect(relDirPrefix('a.txt')).toBe('');
    expect(relDirPrefix('')).toBe('');
  });

  it('反斜杠路径按 Windows 习惯切开', () => {
    expect(relDirPrefix('dir\\sub\\a.txt')).toBe('dir/sub/');
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

  it('改过名后按新名字拼路径，目录层级保持不变', () => {
    const staged = stageSources([{ file: fileOf('b.txt'), rel: 'dir/b.txt' }], 'vol', null);
    expect(stagedDisplayPath(withName(staged[0]!, ' renamed.txt '))).toBe('dir/renamed.txt');
  });
});

describe('validateUploadName', () => {
  it('普通名字通过', () => {
    expect(validateUploadName('report.txt')).toBeNull();
    expect(validateUploadName('  带空格的名字.txt  ')).toBeNull();
    expect(validateUploadName('.env')).toBeNull();
  });

  it('空名与点目录被拒', () => {
    expect(validateUploadName('')).toBe('empty');
    expect(validateUploadName('   ')).toBe('empty');
    expect(validateUploadName('.')).toBe('empty');
    expect(validateUploadName('..')).toBe('empty');
  });

  it('路径分隔符与 NUL 被拒', () => {
    expect(validateUploadName('a/b.txt')).toBe('separator');
    expect(validateUploadName('a\\b.txt')).toBe('separator');
    expect(validateUploadName('a\u0000b.txt')).toBe('separator');
  });

  it('超过 255 字节被拒，正好 255 通过', () => {
    expect(validateUploadName('a'.repeat(255))).toBeNull();
    expect(validateUploadName('a'.repeat(256))).toBe('too_long');
    // 中文按 UTF-8 三字节算：85 字正好 255，86 字就越过上限
    expect(validateUploadName('名'.repeat(85))).toBeNull();
    expect(validateUploadName('名'.repeat(86))).toBe('too_long');
  });
});

describe('withName', () => {
  it('只换生效文件名并去掉两端空白', () => {
    const staged = stageSources([{ file: fileOf('b.txt'), rel: 'dir/b.txt' }], 'vol', 'p1');
    const renamed = withName(staged[0]!, '  new name.txt ');
    expect(renamed.name).toBe('new name.txt');
    expect(renamed.file).toBe(staged[0]!.file);
    expect(renamed.id).toBe(staged[0]!.id);
    expect(renamed.rel).toBe('dir/b.txt');
    expect(renamed.volumeId).toBe('vol');
    expect(renamed.parentId).toBe('p1');
  });
});

describe('uploadArgs', () => {
  it('没有相对路径时只交名字，rel 留空', () => {
    const staged = stageSources([{ file: fileOf('a.txt'), rel: '' }], 'vol', null);
    expect(uploadArgs(withName(staged[0]!, 'b.txt'))).toEqual({ rel: '', name: 'b.txt' });
  });

  it('目录内的文件把新名字写进 rel，因为后端此时忽略 name', () => {
    const staged = stageSources([{ file: fileOf('b.txt'), rel: 'dir/b.txt' }], 'vol', null);
    expect(uploadArgs(withName(staged[0]!, 'c.txt'))).toEqual({
      rel: 'dir/c.txt',
      name: 'c.txt',
    });
  });

  it('没改名时 rel 与原始相对路径一致', () => {
    const staged = stageSources([{ file: fileOf('b.txt'), rel: 'dir/b.txt' }], 'vol', null);
    expect(uploadArgs(staged[0]!)).toEqual({ rel: 'dir/b.txt', name: 'b.txt' });
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

  it('改过名的项按新名字建任务，图标分类也跟新名字走', () => {
    const staged = stageSources([{ file: fileOf('clip.mp4', 64), rel: 'dir/clip.mp4' }], 'vol', null);
    const tasks = toTransferTasks([withName(staged[0]!, 'note.txt')], 'batch-2');
    expect(tasks[0]).toMatchObject({ fileName: 'note.txt', category: 'document' });
  });
});
