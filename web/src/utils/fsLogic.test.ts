import { describe, expect, it } from 'vitest';
import type { FileItem, TransferTask, VolumeInfo } from '../types';
import {
  DEFAULT_UPLOAD_CHUNK_BYTES,
  MAX_SINGLE_FILE_BYTES,
  apportionPercents,
  buildBreadcrumbs,
  canMoveItemsToFolder,
  collectRecursiveDeleteIds,
  computeDirectoryStats,
  computeOverviewSummary,
  formatBytes,
  getItemFullPath,
  inferFileCategory,
  searchFilesRecursively,
  sortFileItems,
  summarizeUploadBatch,
  validateAndPlanUpload,
  volumeDisplayName,
} from './fsLogic';

// 构造 FileItem 的最小工厂：只填必填字段，可选字段按需覆盖
function item(partial: Partial<FileItem> & Pick<FileItem, 'id' | 'name'>): FileItem {
  return {
    volumeId: 'v1',
    parentId: null,
    isDir: false,
    size: 1,
    updatedAt: '2026-01-01T00:00:00Z',
    category: 'document',
    ...partial,
  };
}

// 真实数据形态：卷根不是 FileItem，根层条目的 parentId 为 null
const files: FileItem[] = [
  item({ id: 'docs', name: 'docs', isDir: true, parentId: null, size: 0 }),
  item({ id: 'pics', name: 'pics', isDir: true, parentId: null, size: 0 }),
  item({ id: 'a', name: 'a.txt', parentId: 'docs', size: 100 }),
  item({ id: 'b', name: 'b.txt', parentId: 'docs', size: 200 }),
  item({ id: 'c', name: 'c.png', parentId: 'pics', size: 300, category: 'image' }),
  item({ id: 'loose', name: 'loose.bin', parentId: null, size: 50 }),
];

describe('formatBytes（无 locale 的 ASCII 模式）', () => {
  it.each([
    [0, '0 B'],
    [-5, '0 B'],
    [1, '1 B'],
    [1024, '1.00 KB'],
    [1536, '1.50 KB'],
    [1024 * 1024, '1.00 MB'],
    [2 * 1024 * 1024 * 1024, '2.00 GB'],
  ])('%d -> %s', (bytes, want) => {
    expect(formatBytes(bytes)).toBe(want);
  });
});

describe('inferFileCategory', () => {
  it.each([
    ['x.JPG', 'image'],
    ['a.jpeg', 'image'],
    ['v.mkv', 'video'],
    ['s.flac', 'audio'],
    ['r.pdf', 'pdf'],
    ['z.7z', 'archive'],
    ['noext', 'document'],
    ['weird.unknown', 'document'],
  ])('%s -> %s', (name, want) => {
    expect(inferFileCategory(name)).toBe(want);
  });
  it('目录恒为 folder', () => {
    expect(inferFileCategory('whatever.txt', true)).toBe('folder');
  });
});

describe('validateAndPlanUpload', () => {
  it('空文件名拒绝', () => {
    const r = validateAndPlanUpload('  ', 10);
    expect(r.valid).toBe(false);
    expect(r.errorCode).toBe('EMPTY_NAME');
  });

  it('超过 2GB 拒绝', () => {
    const r = validateAndPlanUpload('big.iso', MAX_SINGLE_FILE_BYTES + 1);
    expect(r.valid).toBe(false);
    expect(r.errorCode).toBe('FILE_TOO_LARGE');
  });

  it('恰好 2GB 允许且走分片续传', () => {
    const r = validateAndPlanUpload('big.iso', MAX_SINGLE_FILE_BYTES);
    expect(r.valid).toBe(true);
    expect(r.isChunkedResume).toBe(true);
    expect(r.chunkSize).toBe(DEFAULT_UPLOAD_CHUNK_BYTES);
    expect(r.totalChunks).toBe(Math.ceil(MAX_SINGLE_FILE_BYTES / DEFAULT_UPLOAD_CHUNK_BYTES));
  });

  it('小于 8MB 单块直传', () => {
    const r = validateAndPlanUpload('s.bin', 4096);
    expect(r.valid).toBe(true);
    expect(r.isChunkedResume).toBe(false);
    expect(r.chunkSize).toBe(4096);
    expect(r.totalChunks).toBe(1);
  });

  it('分片数向上取整且覆盖全部字节', () => {
    const size = DEFAULT_UPLOAD_CHUNK_BYTES * 2 + 1;
    const r = validateAndPlanUpload('m.bin', size);
    expect(r.totalChunks).toBe(3);
    expect(r.totalChunks * r.chunkSize).toBeGreaterThanOrEqual(size);
  });
});

describe('computeDirectoryStats', () => {
  it('rootFolderId=null 统计整卷', () => {
    expect(computeDirectoryStats(files, 'v1', null)).toEqual({
      fileCount: 4,
      folderCount: 2,
      totalBytes: 650,
    });
  });

  it('从指定目录递归统计', () => {
    expect(computeDirectoryStats(files, 'v1', 'docs')).toEqual({
      fileCount: 2,
      folderCount: 0,
      totalBytes: 300,
    });
  });

  it('只统计本卷，不串卷', () => {
    const other = [...files, item({ id: 'x', name: 'x.bin', parentId: null, volumeId: 'v2', size: 9 })];
    expect(computeDirectoryStats(other, 'v1', null).totalBytes).toBe(650);
  });
});

describe('collectRecursiveDeleteIds / canMoveItemsToFolder', () => {
  it('递归收集子树（含自身）', () => {
    expect(collectRecursiveDeleteIds(files, ['docs'])).toEqual(new Set(['docs', 'a', 'b']));
  });

  it('移进自己或自己的后代被拒绝', () => {
    expect(canMoveItemsToFolder(files, ['docs'], 'docs')).toBe(false);
    expect(canMoveItemsToFolder(files, ['docs'], 'a')).toBe(false);
  });

  it('移到平级目录或卷根允许', () => {
    expect(canMoveItemsToFolder(files, ['docs'], 'pics')).toBe(true);
    expect(canMoveItemsToFolder(files, ['docs'], null)).toBe(true);
  });
});

describe('sortFileItems', () => {
  it('目录恒在前，文件按名称升序', () => {
    const sorted = sortFileItems([files[2]!, files[0]!, files[4]!], 'name', 'asc');
    expect(sorted.map((f) => f.id)).toEqual(['docs', 'a', 'c']);
  });

  it('size 排序按字节，desc 反转', () => {
    const asc = sortFileItems([files[2]!, files[3]!, files[5]!], 'size', 'asc');
    const desc = sortFileItems([files[2]!, files[3]!, files[5]!], 'size', 'desc');
    expect(asc.map((f) => f.id)).toEqual(['loose', 'a', 'b']);
    expect(desc.map((f) => f.id)).toEqual(['b', 'a', 'loose']);
  });

  it('updatedAt 排序', () => {
    const items = [
      item({ id: 'old', name: 'old', updatedAt: '2025-01-01T00:00:00Z' }),
      item({ id: 'new', name: 'new', updatedAt: '2026-01-01T00:00:00Z' }),
    ];
    expect(sortFileItems(items, 'updatedAt', 'asc').map((f) => f.id)).toEqual(['old', 'new']);
  });

  it('目录占用的字节参与 size 排序，平局按名称', () => {
    // docs 与 pics 子树都是 300B：按名称升序决定先后
    const sorted = sortFileItems([files[1]!, files[0]!], 'size', 'asc', files);
    expect(sorted.map((f) => f.id)).toEqual(['docs', 'pics']);
  });
});

describe('searchFilesRecursively', () => {
  it('空查询返回当前目录的直接子项', () => {
    expect(searchFilesRecursively(files, 'v1', '', 'docs').map((f) => f.id)).toEqual(['a', 'b']);
  });

  it('非空查询递归全卷匹配', () => {
    expect(searchFilesRecursively(files, 'v1', 'TXT').map((f) => f.id)).toEqual(['a', 'b']);
  });

  it('非空查询限定在起始目录子树内', () => {
    expect(searchFilesRecursively(files, 'v1', 'c', 'docs')).toEqual([]);
    expect(searchFilesRecursively(files, 'v1', 'c', 'pics').map((f) => f.id)).toEqual(['c']);
  });
});

describe('buildBreadcrumbs / getItemFullPath', () => {
  it('根层目录的面包屑就是它自己', () => {
    const crumbs = buildBreadcrumbs(files, 'v1', 'docs');
    expect(crumbs.map((c) => c.id)).toEqual(['docs']);
  });

  it('卷根文件路径无前缀斜杠叠加', () => {
    expect(getItemFullPath(files, files[2]!)).toBe('/docs/a.txt');
  });

  it('根层文件路径无前缀', () => {
    expect(getItemFullPath(files, files[5]!)).toBe('/loose.bin');
  });

  it('环不致死：visited 断路', () => {
    const cyc = [
      item({ id: 'x', name: 'x', isDir: true, parentId: 'y' }),
      item({ id: 'y', name: 'y', isDir: true, parentId: 'x' }),
    ];
    expect(buildBreadcrumbs(cyc, 'v1', 'x').map((c) => c.id).length).toBeLessThanOrEqual(2);
  });
});

describe('apportionPercents（最大余数法）', () => {
  it('和恒为 100', () => {
    expect(apportionPercents([1, 2, 3])).toEqual([17, 33, 50]);
  });

  it('全 0 输入全 0 输出', () => {
    expect(apportionPercents([0, 0, 0])).toEqual([0, 0, 0]);
  });

  it('空输入返回空数组', () => {
    expect(apportionPercents([])).toEqual([]);
  });

  it('99.999%+0.001% 两卷只能 100/0', () => {
    expect(apportionPercents([9999, 1])).toEqual([100, 0]);
  });

  it('单卷恒 100', () => {
    expect(apportionPercents([42])).toEqual([100]);
  });

  it('各部分非负且总和恒 100（随机化）', () => {
    for (let seed = 1; seed <= 50; seed += 1) {
      const parts = Array.from({ length: 6 }, (_, i) => ((seed * (i + 3)) % 17) + 1);
      const out = apportionPercents(parts);
      expect(out.reduce((a, b) => a + b, 0)).toBe(100);
      expect(out.every((v) => v >= 0)).toBe(true);
    }
  });
});

describe('computeOverviewSummary', () => {
  const volumes: VolumeInfo[] = [{ id: 'v1', name: 'demo' }, { id: 'v2', name: 'demo2' }];

  it('各卷合计与占比', () => {
    const s = computeOverviewSummary(volumes, [
      item({ id: 'a', name: 'a', volumeId: 'v1', size: 999 }),
      item({ id: 'b', name: 'b', volumeId: 'v1', size: 1 }),
      item({ id: 'c', name: 'c', volumeId: 'v2', size: 1000 }),
    ]);
    expect(s.totalVolumes).toBe(2);
    expect(s.totalFiles).toBe(3);
    expect(s.totalBytes).toBe(2000);
    expect(s.perVolume.map((p) => p.sharePct).reduce((a, b) => a + b, 0)).toBe(100);
    expect(s.perVolume.map((p) => p.sharePct).sort()).toEqual([50, 50]);
  });

  it('空卷也占一格且占比 0', () => {
    const s = computeOverviewSummary(volumes, [item({ id: 'a', name: 'a', volumeId: 'v1', size: 10 })]);
    const empty = s.perVolume.find((p) => p.volume.id === 'v2');
    expect(empty?.fileCount).toBe(0);
    expect(empty?.sharePct).toBe(0);
    expect(s.perVolume.reduce((acc, p) => acc + p.sharePct, 0)).toBe(100);
  });
});

describe('volumeDisplayName', () => {
  it('有频道标题时显示「标题 (卷名)」', () => {
    expect(volumeDisplayName('demo', '我的盘')).toBe('我的盘 (demo)');
  });

  it('没有标题时只显示卷名', () => {
    expect(volumeDisplayName('demo')).toBe('demo');
    expect(volumeDisplayName('demo', '')).toBe('demo');
    expect(volumeDisplayName('demo', '   ')).toBe('demo');
  });

  it('标题与卷名相同时不重复显示', () => {
    expect(volumeDisplayName('demo', 'demo')).toBe('demo');
  });
});

// transfer 构造上传任务：只填汇总用得上的字段
function transfer(partial: Partial<TransferTask> & Pick<TransferTask, 'id'>): TransferTask {
  return {
    fileName: `${partial.id}.bin`,
    volumeId: 'v1',
    targetParentId: null,
    direction: 'upload',
    size: 100,
    transferredBytes: 0,
    state: 'queued',
    speedBps: 0,
    isChunkedResume: false,
    chunkSize: 0,
    currentChunk: 0,
    totalChunks: 1,
    createdAt: '2026-01-01T00:00:00Z',
    ...partial,
  };
}

describe('summarizeUploadBatch', () => {
  it('同批次汇总已传字节与总量', () => {
    const s = summarizeUploadBatch([
      transfer({ id: 'a', batchId: 'b1', size: 100, transferredBytes: 50 }),
      transfer({ id: 'b', batchId: 'b1', size: 300, transferredBytes: 300 }),
    ]);
    expect(s).toEqual({ files: 2, done: 350, total: 400 });
  });

  it('多个批次时只看最新的一批', () => {
    const s = summarizeUploadBatch([
      transfer({ id: 'old', batchId: 'b1', size: 10, transferredBytes: 10 }),
      transfer({ id: 'old2', batchId: 'b1', size: 10, transferredBytes: 10 }),
      transfer({ id: 'n2', batchId: 'b2', size: 200, transferredBytes: 0, createdAt: '2026-02-01T00:00:00Z' }),
      transfer({ id: 'n1', batchId: 'b2', size: 200, transferredBytes: 20, createdAt: '2026-02-01T00:00:00Z' }),
    ]);
    expect(s).toEqual({ files: 2, done: 20, total: 400 });
  });

  it('单文件批次不显示汇总', () => {
    expect(summarizeUploadBatch([transfer({ id: 'a', batchId: 'b1' })])).toBeNull();
  });

  it('没有批次号时返回 null', () => {
    expect(summarizeUploadBatch([transfer({ id: 'a' }), transfer({ id: 'b' })])).toBeNull();
    expect(summarizeUploadBatch([])).toBeNull();
  });

  it('已传字节超出大小时按大小封顶', () => {
    const s = summarizeUploadBatch([
      transfer({ id: 'a', batchId: 'b1', size: 100, transferredBytes: 180 }),
      transfer({ id: 'b', batchId: 'b1', size: 100, transferredBytes: 100 }),
    ]);
    expect(s).toEqual({ files: 2, done: 200, total: 200 });
  });
});
