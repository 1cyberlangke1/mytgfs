// @vitest-environment jsdom
//
// DownloadsView 的组件测试：任务行状态/按钮、进度条、行内删除二次确认。
//
// 输入：假任务与假文件列表，回调用 vi.fn()
// 输出：断言渲染与回调
// 预期：不碰网络；删除必须点两次（先确认再删），预览/刷新/打开目录各发一次回调
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';

import { DownloadsView } from './DownloadsView';
import { DICTIONARY } from '../utils/i18n';
import type { Translations } from '../utils/i18n';
import type { DownloadItem, DownloadTask } from '../types';

const t = DICTIONARY.zh as Translations;

// vitest 没开 globals，testing-library 不会自动清理：不手动清会让上一个用例的 DOM 留在页面里
afterEach(cleanup);

const makeTask = (patch: Partial<DownloadTask>): DownloadTask => ({
  id: 'task-1',
  name: 'clip.mp4',
  vol: 'demo',
  path: '/demo/clip.mp4',
  size: 1000,
  done: 250,
  state: 'running',
  ...patch,
});

const renderView = (files: DownloadItem[], tasks: DownloadTask[] = []) => {
  const handlers = {
    onRefresh: vi.fn(),
    onDirAction: vi.fn(),
    onRemove: vi.fn(),
    onTask: vi.fn(),
    onPreview: vi.fn(),
  };
  render(
    <DownloadsView
      t={t}
      locale="zh"
      dir="D:/mytgfs/downloads"
      files={files}
      tasks={tasks}
      onRefresh={handlers.onRefresh}
      dirAction="open"
      onDirAction={handlers.onDirAction}
      onRemove={handlers.onRemove}
      onTask={handlers.onTask}
      onPreview={handlers.onPreview}
    />
  );
  return handlers;
};

describe('DownloadsView', () => {
  it('进行中的任务显示百分比并可暂停/取消', () => {
    const handlers = renderView([], [makeTask({})]);

    expect(screen.getByText('clip.mp4')).toBeTruthy();
    const bar = screen.getByRole('progressbar', { name: 'clip.mp4' });
    expect(bar.getAttribute('aria-valuenow')).toBe('25');

    fireEvent.click(screen.getByRole('button', { name: t.downloads.taskPause }));
    expect(handlers.onTask).toHaveBeenCalledWith('task-1', 'pause');

    fireEvent.click(screen.getByRole('button', { name: t.downloads.taskCancel }));
    expect(handlers.onTask).toHaveBeenCalledWith('task-1', 'cancel');
  });

  it('暂停中的任务给的是继续按钮，失败任务显示原因', () => {
    const handlers = renderView(
      [],
      [
        makeTask({ id: 'task-2', state: 'paused', done: 0 }),
        makeTask({ id: 'task-3', state: 'failed', error: '网络断了' }),
      ]
    );

    fireEvent.click(screen.getAllByRole('button', { name: t.downloads.taskResume })[0]!);
    expect(handlers.onTask).toHaveBeenCalledWith('task-2', 'resume');
    expect(screen.getByText('网络断了')).toBeTruthy();
  });

  it('删除要先确认，确认后只回调一次', () => {
    const handlers = renderView([
      { name: 'cat.jpg', size: 2048, mod: '2026-11-15T10:00:00Z', path: 'D:/d/cat.jpg' },
    ]);

    fireEvent.click(screen.getByRole('button', { name: t.downloads.removeBtn }));
    expect(handlers.onRemove).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: t.downloads.confirmDeleteBtn }));
    expect(handlers.onRemove).toHaveBeenCalledTimes(1);
    expect(handlers.onRemove).toHaveBeenCalledWith('cat.jpg');
  });

  it('预览/刷新/打开目录各自触发一次回调', () => {
    const handlers = renderView([
      { name: 'note.txt', size: 12, mod: '2026-11-15T10:00:00Z', path: 'D:/d/note.txt' },
    ]);

    fireEvent.click(screen.getByRole('button', { name: t.downloads.previewBtn }));
    expect(handlers.onPreview).toHaveBeenCalledWith('note.txt');

    fireEvent.click(screen.getByRole('button', { name: t.downloads.refreshBtn }));
    expect(handlers.onRefresh).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByRole('button', { name: t.downloads.openDirBtn }));
    expect(handlers.onDirAction).toHaveBeenCalledTimes(1);
  });

  it('导出模式下按钮改名并走同一个回调', () => {
    const handlers = {
      onRefresh: vi.fn(),
      onDirAction: vi.fn(),
      onRemove: vi.fn(),
      onTask: vi.fn(),
      onPreview: vi.fn(),
    };
    render(
      <DownloadsView
        t={t}
        locale="zh"
        dir="/data/user/0/com.mytgfs.app/files/data/downloads"
        files={[{ name: 'note.txt', size: 12, mod: '2026-11-15T10:00:00Z', path: '/data/x/note.txt' }]}
        tasks={[]}
        onRefresh={handlers.onRefresh}
        dirAction="export"
        onDirAction={handlers.onDirAction}
        onRemove={handlers.onRemove}
        onTask={handlers.onTask}
        onPreview={handlers.onPreview}
      />
    );

    expect(screen.queryByRole('button', { name: t.downloads.openDirBtn })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: t.downloads.exportDirBtn }));
    expect(handlers.onDirAction).toHaveBeenCalledTimes(1);
  });

  it('导出模式可以只导出选中的文件', () => {
    const onDirAction = vi.fn();
    const files: DownloadItem[] = [
      { name: 'a.txt', size: 12, mod: '2026-11-15T10:00:00Z', path: '/data/x/a.txt' },
      { name: 'b.txt', size: 34, mod: '2026-11-15T10:01:00Z', path: '/data/x/b.txt' },
    ];
    render(
      <DownloadsView
        t={t}
        locale="zh"
        dir="/data/user/0/com.mytgfs.app/files/data/downloads"
        files={files}
        tasks={[]}
        onRefresh={vi.fn()}
        dirAction="export"
        onDirAction={onDirAction}
        onRemove={vi.fn()}
        onTask={vi.fn()}
        onPreview={vi.fn()}
      />
    );

    // 不选中时按钮仍是「全部导出」
    expect(screen.getByRole('button', { name: t.downloads.exportDirBtn })).toBeTruthy();

    fireEvent.click(screen.getByRole('checkbox', { name: `${t.downloads.exportSelectLabel} a.txt` }));
    fireEvent.click(
      screen.getByRole('button', { name: t.downloads.exportSelectedBtn.replace('{count}', '1') })
    );
    expect(onDirAction).toHaveBeenCalledWith(['a.txt']);

    // 全选后一次带出两个名字
    fireEvent.click(screen.getByRole('checkbox', { name: t.downloads.exportSelectAll }));
    fireEvent.click(
      screen.getByRole('button', { name: t.downloads.exportSelectedBtn.replace('{count}', '2') })
    );
    expect(onDirAction).toHaveBeenLastCalledWith(['a.txt', 'b.txt']);
  });

  it('空列表显示空态', () => {
    renderView([]);
    expect(screen.getByText(t.downloads.emptyTitle)).toBeTruthy();
  });
});
