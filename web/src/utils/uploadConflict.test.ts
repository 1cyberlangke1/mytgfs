// 同名冲突检测与消解的纯函数测试。
//
// 输入：手工构造的目录树、草稿项与「目录里已有的名字」；
// 输出：目录键、冲突列表、建议名，以及按选择处理后的清单；
// 预期：比较的是卷内真实路径，目录上传不会与根目录同名文件误判冲突。
import { describe, expect, it } from 'vitest';
import { stageSources, type StagedUpload } from './uploadPlan';
import {
  buildDirIndex,
  detectConflicts,
  resolveUploadConflicts,
  uniqueName,
  type TreeEntry,
} from './uploadConflict';

// stagedOf 造一组指定卷/目录/相对路径的草稿项。
function stagedOf(names: string[], volumeId = 'vol', parentId: string | null = null) {
  return stageSources(
    names.map((name) => ({ file: new File([new Uint8Array(4)], name), rel: '' })),
    volumeId,
    parentId
  );
}

// indexOf 建索引并算出草稿行，模拟 App 的调用方式。
function indexOf(entries: readonly TreeEntry[], items: readonly StagedUpload[]) {
  const index = buildDirIndex(entries);
  return {
    index,
    rows: items.map((item) => ({ id: item.id, name: item.name, dirKey: index.keyOf(item) })),
  };
}

const rootFile = (id: string, name: string, volumeId = 'vol'): TreeEntry => ({
  id,
  volumeId,
  parentId: null,
  name,
});

const dir = (id: string, name: string, parentId: string | null = null): TreeEntry => ({
  id,
  volumeId: 'vol',
  parentId,
  name,
});

describe('uniqueName', () => {
  it('撞名时插在扩展名前并跳过已占用编号', () => {
    expect(uniqueName('a.txt', new Set(['a.txt', 'a (2).txt']))).toBe('a (3).txt');
    expect(uniqueName('a.txt', new Set(['a.txt']))).toBe('a (2).txt');
  });

  it('没有扩展名时整体当基名', () => {
    expect(uniqueName('notes', new Set(['notes']))).toBe('notes (2)');
    expect(uniqueName('.env', new Set(['.env']))).toBe('.env (2)');
  });

  it('不冲突时原样返回', () => {
    expect(uniqueName('a.txt', new Set())).toBe('a.txt');
  });
});

describe('buildDirIndex.keyOf', () => {
  it('根目录落点与子目录落点分开', () => {
    const entries = [dir('d1', 'docs')];
    const [rootItem] = stagedOf(['a.txt'], 'vol', null);
    const [nestedItem] = stagedOf(['a.txt'], 'vol', 'd1');
    const { index } = indexOf(entries, [rootItem!, nestedItem!]);
    expect(index.keyOf(rootItem!)).not.toBe(index.keyOf(nestedItem!));
  });

  it('父目录路径与 rel 前缀按后端语义直接拼接', () => {
    const entries = [dir('d1', 'docs'), dir('d2', 'sub', 'd1')];
    const [item] = stageSources([{ file: new File(['x'], 'a.txt'), rel: 'docs/sub/a.txt' }], 'vol', 'd2');
    const { index } = indexOf(entries, [item!]);
    // 与 uploadTarget 一致：dir 是父目录路径，rel 再拼在它后面
    expect(index.keyOf(item!)).toBe('vol\u0000docs/sub/docs/sub');
  });

  it('父目录缺失时回落到卷根', () => {
    const [item] = stagedOf(['a.txt'], 'vol', 'missing');
    const { index } = indexOf([], [item!]);
    expect(index.keyOf(item!)).toBe('vol\u0000');
  });
});

describe('detectConflicts', () => {
  it('与目标目录已有文件重名才报冲突', () => {
    const items = stagedOf(['a.txt', 'b.txt']);
    const { index, rows } = indexOf([rootFile('n1', 'a.txt')], items);
    const conflicts = detectConflicts(rows, index.namesByDir);
    expect(conflicts.map((c) => c.name)).toEqual(['a.txt']);
    expect(conflicts[0]!.suggested).toBe('a (2).txt');
  });

  it('目录上传不与根目录同名文件误判', () => {
    const entries = [rootFile('n1', 'a.txt'), dir('d1', 'docs')];
    const [item] = stageSources([{ file: new File(['x'], 'a.txt'), rel: 'docs/a.txt' }], 'vol', null);
    const { index, rows } = indexOf(entries, [item!]);
    expect(detectConflicts(rows, index.namesByDir)).toEqual([]);
  });

  it('目录内已有同名文件时按该目录比较', () => {
    const entries = [dir('d1', 'docs'), { ...rootFile('n2', 'a.txt'), parentId: 'd1' }];
    const [item] = stageSources([{ file: new File(['x'], 'a.txt'), rel: 'docs/a.txt' }], 'vol', null);
    const { index, rows } = indexOf(entries, [item!]);
    const conflicts = detectConflicts(rows, index.namesByDir);
    expect(conflicts).toHaveLength(1);
    expect(conflicts[0]!.suggested).toBe('a (2).txt');
  });

  it('同批内重名只让后一个改名，避免两个都悄悄覆盖同一路径', () => {
    const items = stagedOf(['same.txt', 'same.txt']);
    const { index, rows } = indexOf([], items);
    const conflicts = detectConflicts(rows, index.namesByDir);
    expect(conflicts).toHaveLength(1);
    expect(conflicts[0]!.suggested).toBe('same (2).txt');
    expect(conflicts[0]!.id).toBe(items[1]!.id);
  });
});

describe('resolveUploadConflicts', () => {
  it('改名换成建议名，覆盖保留原名，跳过移除', () => {
    const items = stagedOf(['a.txt', 'b.txt', 'c.txt']);
    const conflicts = [
      { id: items[0]!.id, name: 'a.txt', suggested: 'a (2).txt' },
      { id: items[1]!.id, name: 'b.txt', suggested: 'b (2).txt' },
      { id: items[2]!.id, name: 'c.txt', suggested: 'c (2).txt' },
    ];
    const out = resolveUploadConflicts(items, conflicts, {
      [items[0]!.id]: 'rename',
      [items[1]!.id]: 'overwrite',
      [items[2]!.id]: 'skip',
    });
    expect(out.map((item) => item.name)).toEqual(['a (2).txt', 'b.txt']);
  });

  it('缺省动作按改名处理', () => {
    const items = stagedOf(['a.txt']);
    const conflicts = [{ id: items[0]!.id, name: 'a.txt', suggested: 'a (2).txt' }];
    expect(resolveUploadConflicts(items, conflicts, {})[0]!.name).toBe('a (2).txt');
  });
});
