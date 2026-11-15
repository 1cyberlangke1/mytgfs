// @vitest-environment jsdom
//
// OverviewView 的组件测试：概览数字、卷卡、操作队列行与批量上传汇总条。
//
// 输入：假的卷/文件/任务列表，回调都是 vi.fn()
// 输出：断言渲染文本、进度条语义与回调参数
// 预期：不碰网络；失败原因必须渲染；q- 前缀的队列任务不出现本地重试/取消按钮
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';

import { OverviewView } from './OverviewView';
import { DICTIONARY } from '../utils/i18n';
import type { Translations } from '../utils/i18n';
import type { FileItem, TransferTask, VolumeInfo } from '../types';

const t = DICTIONARY.zh as Translations;

afterEach(cleanup);

const volume = (id: string, name: string, title?: string): VolumeInfo => ({
  id,
  name,
  title,
});

const file = (id: string, volumeId: string, name: string, size: number): FileItem => ({
  id,
  volumeId,
  parentId: null,
  name,
  isDir: false,
  size,
  updatedAt: '2026-11-15T10:00:00Z',
  category: 'archive',
});

const task = (patch: Partial<TransferTask>): TransferTask => ({
  id: 'task-1',
  fileName: 'clip.mp4',
  volumeId: 'demo',
  targetParentId: null,
  direction: 'upload',
  size: 1000,
  transferredBytes: 250,
  state: 'transferring',
  speedBps: 0,
  isChunkedResume: false,
  chunkSize: 0,
  currentChunk: 0,
  totalChunks: 0,
  createdAt: '2026-11-15T10:00:00Z',
  opKind: 'upload',
  ...patch,
});

const renderView = (patch: {
  volumes?: VolumeInfo[];
  files?: FileItem[];
  transfers?: TransferTask[];
}) => {
  const handlers = {
    onSelectVolumeAndBrowse: vi.fn(),
    onUploadFiles: vi.fn(),
    onToggleTransferPause: vi.fn(),
    onRetryTransfer: vi.fn(),
    onCancelTransfer: vi.fn(),
    onClearCompletedTransfers: vi.fn(),
  };
  render(
    <OverviewView
      t={t}
      locale="zh"
      volumes={patch.volumes ?? []}
      files={patch.files ?? []}
      transfers={patch.transfers ?? []}
      activeVolumeId={patch.volumes?.[0]?.id ?? ''}
      onSelectVolumeAndBrowse={handlers.onSelectVolumeAndBrowse}
      onUploadFiles={handlers.onUploadFiles}
      onToggleTransferPause={handlers.onToggleTransferPause}
      onRetryTransfer={handlers.onRetryTransfer}
      onCancelTransfer={handlers.onCancelTransfer}
      onClearCompletedTransfers={handlers.onClearCompletedTransfers}
    />
  );
  return handlers;
};

describe('OverviewView', () => {
  it('概览数字按卷与文件汇总，卷卡显示「标题 (name)」并能进入', () => {
    const handlers = renderView({
      volumes: [volume('demo', 'demo', '演示卷'), volume('sezhijian', 'sezhijian')],
      files: [file('f1', 'demo', 'a.bin', 1500), file('f2', 'demo', 'b.bin', 500)],
    });

    expect(screen.getByTestId('summary-total-volumes').textContent).toBe('2');
    expect(screen.getByTestId('summary-total-files').textContent).toBe('2');
    expect(screen.getByTestId('summary-total-bytes').textContent).toContain('KB');

    // 有标题时显示「标题 (name)」，没标题只显示 name
    expect(screen.getByText('演示卷 (demo)')).toBeTruthy();
    expect(screen.getByText('sezhijian')).toBeTruthy();

    fireEvent.click(screen.getAllByRole('button', { name: t.overview.browseVolumeBtn })[0]!);
    expect(handlers.onSelectVolumeAndBrowse).toHaveBeenCalledWith('demo');
  });

  it('没有卷时显示空态', () => {
    renderView({ volumes: [] });
    expect(screen.getByText(t.overview.emptyVolumesTitle)).toBeTruthy();
    expect(screen.getByText(t.overview.emptyTransfersTitle)).toBeTruthy();
  });

  it('队列行显示类型徽章与失败原因，失败上传可重试', () => {
    const handlers = renderView({
      volumes: [volume('demo', 'demo')],
      transfers: [
        task({
          id: 'fail-1',
          fileName: 'broken.mp3',
          state: 'failed',
          errorMessage: 'FILE_PARTS_INVALID',
          transferredBytes: 0,
        }),
      ],
    });

    expect(screen.getByText(t.overview.queueKind.upload)).toBeTruthy();
    expect(screen.getByText(t.overview.transferFailed)).toBeTruthy();
    expect(screen.getByText('FILE_PARTS_INVALID')).toBeTruthy();
    expect(screen.getByRole('progressbar', { name: 'broken.mp3' }).getAttribute('aria-valuenow')).toBe(
      '0'
    );

    fireEvent.click(screen.getByRole('button', { name: `${t.overview.retryBtn} broken.mp3` }));
    expect(handlers.onRetryTransfer).toHaveBeenCalledWith('fail-1');

    fireEvent.click(screen.getByRole('button', { name: `${t.overview.cancelBtn} broken.mp3` }));
    expect(handlers.onCancelTransfer).toHaveBeenCalledWith('fail-1');
  });

  it('服务端队列任务（q- 前缀）不给本地重试/取消/暂停', () => {
    const handlers = renderView({
      volumes: [volume('demo', 'demo')],
      transfers: [
        task({
          id: 'q-42',
          fileName: '/batch',
          state: 'transferring',
          opKind: 'mkdir',
          size: 0,
          transferredBytes: 0,
        }),
      ],
    });

    expect(screen.getByText(t.overview.queueKind.mkdir)).toBeTruthy();
    expect(screen.getByText(t.overview.queueRunning)).toBeTruthy();
    expect(screen.queryByRole('button', { name: `${t.overview.retryBtn} /batch` })).toBeNull();
    // 取消按钮仍在 DOM 里，但被 Tailwind 的 hidden 类藏起来（jsdom 不加载 CSS，只能看 class）
    const cancel = screen.getByRole('button', { name: `${t.overview.cancelBtn} /batch` });
    expect(cancel.className).toContain('hidden');
    // 服务端队列任务没有本地暂停入口
    expect(screen.queryByRole('button', { name: `${t.overview.pauseBtn} /batch` })).toBeNull();
    // 大小未知时不渲染进度条，避免假装 0%
    expect(screen.queryByRole('progressbar', { name: '/batch' })).toBeNull();
    expect(handlers.onRetryTransfer).not.toHaveBeenCalled();
  });

  it('清理已完成按钮只在有完成/失败任务时出现', () => {
    const idle = renderView({
      volumes: [volume('demo', 'demo')],
      transfers: [task({ id: 'run-1', state: 'transferring' })],
    });
    expect(screen.queryByRole('button', { name: t.overview.clearCompletedBtn })).toBeNull();

    cleanup();

    const done = renderView({
      volumes: [volume('demo', 'demo')],
      transfers: [task({ id: 'done-1', state: 'completed', transferredBytes: 1000 })],
    });
    const clear = screen.getByRole('button', { name: t.overview.clearCompletedBtn });
    fireEvent.click(clear);
    expect(done.onClearCompletedTransfers).toHaveBeenCalledTimes(1);
    expect(idle.onClearCompletedTransfers).not.toHaveBeenCalled();
  });

  it('同批次上传 ≥2 个文件才显示整批总进度', () => {
    renderView({
      volumes: [volume('demo', 'demo')],
      transfers: [
        task({ id: 'u1', fileName: 'a.bin', batchId: 'b1', size: 100, transferredBytes: 100 }),
        task({ id: 'u2', fileName: 'b.bin', batchId: 'b1', size: 100, transferredBytes: 0 }),
      ],
    });

    const batchBar = screen.getByRole('progressbar', {
      name: t.overview.batchUploadLabel.replace('{count}', '2'),
    });
    expect(batchBar.getAttribute('aria-valuenow')).toBe('50');

    cleanup();

    // 单文件批次不显示汇总条
    renderView({
      volumes: [volume('demo', 'demo')],
      transfers: [task({ id: 'u3', fileName: 'c.bin', batchId: 'b2' })],
    });
    expect(
      screen.queryByRole('progressbar', {
        name: t.overview.batchUploadLabel.replace('{count}', '1'),
      })
    ).toBeNull();
  });
});
