import React, { useState } from 'react';
import { FolderOpen, RefreshCw, Trash2, HardDriveDownload, Pause, Play, X, Eye } from 'lucide-react';
import { DownloadItem, DownloadTask } from '../types';
import { Locale } from '../types';
import { formatBytes, inferFileCategory } from '../utils/fsLogic';
import { Translations, formatDateTime, formatPercent } from '../utils/i18n';
import { RichFileTypeEmblem } from './VisualAssets';
import { Button } from './ui';

interface DownloadsViewProps {
  t: Translations;
  locale: Locale;
  dir: string;
  files: DownloadItem[];
  /** 进行中的下载任务；完成的任务会直接从列表里消失 */
  tasks: DownloadTask[];
  /** 刷新列表：服务端可能在别处新增或删除文件 */
  onRefresh: () => void;
  /** 用系统文件管理器打开下载目录 */
  onOpenDir: () => void;
  /** 删除本地副本；返回 Promise 以便页面自行收尾 */
  onRemove: (name: string) => Promise<void> | void;
  /** 暂停、继续或取消一个下载任务 */
  onTask: (id: string, action: 'pause' | 'resume' | 'cancel') => void;
  /** 预览本地文件（图片/音视频/PDF/文本），不重新下载 */
  onPreview: (name: string) => void;
}

// DownloadsView 展示下载目录里的文件与进行中的下载任务。
//
// 与浏览器下载不同，写入由服务端完成，因此页面只需按推送刷新快照。
// 任务支持暂停（保留已落盘字节）、继续（从断点续写）与取消（删临时文件）。
// 删除已完成文件是本地副本操作，采用行内二次确认，避免误删。
export const DownloadsView: React.FC<DownloadsViewProps> = ({
  t,
  locale,
  dir,
  files,
  tasks,
  onRefresh,
  onOpenDir,
  onRemove,
  onTask,
  onPreview,
}) => {
  const [confirming, setConfirming] = useState('');
  const totalBytes = files.reduce((acc, f) => acc + f.size, 0);

  const remove = (name: string) => {
    setConfirming('');
    void onRemove(name);
  };

  // 进度百分比：大小未知（0）时不猜，直接不渲染进度条
  const percentOf = (task: DownloadTask) =>
    task.size > 0 ? Math.min(100, Math.floor((task.done / task.size) * 100)) : 0;

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="space-y-1 min-w-0">
          <h1 className="text-xl sm:text-2xl font-semibold tracking-tight text-zinc-950 dark:text-white">
            {t.downloads.title}
          </h1>
          <p className="text-xs text-zinc-500 dark:text-zinc-400">
            {t.downloads.summary
              .replace('{count}', String(files.length))
              .replace('{size}', formatBytes(totalBytes))}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button type="button" variant="outline" onClick={onRefresh}>
            <RefreshCw className="w-3.5 h-3.5" />
            <span>{t.downloads.refreshBtn}</span>
          </Button>
          <Button type="button" variant="accent" onClick={onOpenDir}>
            <FolderOpen className="w-3.5 h-3.5" />
            <span>{t.downloads.openDirBtn}</span>
          </Button>
        </div>
      </div>

      {tasks.length > 0 && (
        <section className="surface-card rounded-2xl overflow-hidden">
          <h2 className="px-4 pt-3 pb-2 text-[11px] font-medium uppercase tracking-wide text-zinc-500 dark:text-zinc-400">
            {t.downloads.tasksTitle}
          </h2>
          <ul className="divide-y divide-zinc-200/70 dark:divide-zinc-800/70">
            {tasks.map((task) => {
              const pct = percentOf(task);
              const failed = task.state === 'failed';
              return (
                <li key={task.id} className="px-4 py-3 space-y-2">
                  <div className="flex items-center gap-3">
                    <RichFileTypeEmblem category={inferFileCategory(task.name, false)} size="sm" />
                    <div className="min-w-0 flex-1">
                      <p
                        className="text-sm font-medium text-zinc-900 dark:text-zinc-100 truncate"
                        title={task.path}
                      >
                        {task.name}
                      </p>
                      <p className="text-[11px] text-zinc-500 dark:text-zinc-400 font-mono tabular-nums">
                        {formatBytes(task.done)} / {task.size > 0 ? formatBytes(task.size) : '—'}
                        {task.size > 0 && ` · ${formatPercent(pct, locale)}`}
                        {' · '}
                        <span className={failed ? 'text-red-600 dark:text-red-400' : ''}>
                          {task.state === 'running'
                            ? t.downloads.stateRunning
                            : task.state === 'paused'
                              ? t.downloads.statePaused
                              : t.downloads.stateFailed}
                        </span>
                      </p>
                    </div>
                    <div className="flex items-center gap-1.5 shrink-0">
                      {task.state === 'running' ? (
                        <Button
                          type="button"
                          variant="outline"
                          size="icon"
                          aria-label={t.downloads.taskPause}
                          title={t.downloads.taskPause}
                          onClick={() => onTask(task.id, 'pause')}
                        >
                          <Pause className="w-3.5 h-3.5" />
                        </Button>
                      ) : (
                        <Button
                          type="button"
                          variant="outline"
                          size="icon"
                          aria-label={t.downloads.taskResume}
                          title={t.downloads.taskResume}
                          onClick={() => onTask(task.id, 'resume')}
                        >
                          <Play className="w-3.5 h-3.5" />
                        </Button>
                      )}
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        aria-label={t.downloads.taskCancel}
                        title={t.downloads.taskCancel}
                        onClick={() => onTask(task.id, 'cancel')}
                      >
                        <X className="w-3.5 h-3.5" />
                      </Button>
                    </div>
                  </div>
                  {task.size > 0 && (
                    <div className="h-1.5 w-full rounded-full bg-zinc-200/80 dark:bg-zinc-800 overflow-hidden">
                      <div
                        className={`h-full rounded-full transition-[width] duration-300 ${
                          failed ? 'bg-[#FF3B30]' : 'bg-[#007AFF]'
                        }`}
                        style={{ width: `${task.state === 'failed' ? 100 : pct}%` }}
                      />
                    </div>
                  )}
                  {failed && task.error && (
                    <p className="text-[11px] text-red-600 dark:text-red-400 break-all">{task.error}</p>
                  )}
                </li>
              );
            })}
          </ul>
        </section>
      )}

      <section className="surface-card rounded-2xl px-4 py-3">
        <p className="text-[11px] text-zinc-500 dark:text-zinc-400">{t.downloads.dirLabel}</p>
        <p
          dir="ltr"
          className="mt-1 font-mono text-xs text-zinc-800 dark:text-zinc-200 break-all"
          title={dir}
        >
          {dir || '—'}
        </p>
      </section>

      {files.length === 0 ? (
        <section className="surface-card rounded-2xl p-10 text-center space-y-3">
          <div className="w-12 h-12 mx-auto rounded-2xl bg-zinc-100 dark:bg-zinc-800/70 inline-flex items-center justify-center text-zinc-400 dark:text-zinc-500">
            <HardDriveDownload className="w-6 h-6" />
          </div>
          <h2 className="text-sm font-semibold text-zinc-950 dark:text-white">
            {t.downloads.emptyTitle}
          </h2>
          <p className="text-xs text-zinc-500 dark:text-zinc-400 max-w-sm mx-auto">
            {t.downloads.emptyDesc}
          </p>
        </section>
      ) : (
        <section className="surface-card rounded-2xl overflow-hidden">
          <ul className="divide-y divide-zinc-200/70 dark:divide-zinc-800/70">
            {files.map((f) => {
              const confirmingThis = confirming === f.name;
              return (
                <li
                  key={f.name}
                  className="flex items-center gap-3 px-4 py-3 hover:bg-zinc-50 dark:hover:bg-zinc-900/40 transition-colors"
                >
                  <RichFileTypeEmblem category={inferFileCategory(f.name, false)} size="sm" />
                  <div className="min-w-0 flex-1">
                    <p className="text-sm font-medium text-zinc-900 dark:text-zinc-100 truncate" title={f.name}>
                      {f.name}
                    </p>
                    <p className="text-[11px] text-zinc-500 dark:text-zinc-400 font-mono tabular-nums">
                      {formatBytes(f.size)} · {formatDateTime(f.mod, locale)}
                    </p>
                  </div>
                  {confirmingThis ? (
                    <div className="flex items-center gap-2 shrink-0">
                      <span className="text-[11px] text-red-600 dark:text-red-400">
                        {t.downloads.confirmDelete}
                      </span>
                      <Button type="button" variant="destructive" onClick={() => remove(f.name)}>
                        {t.downloads.confirmDeleteBtn}
                      </Button>
                      <Button type="button" variant="outline" onClick={() => setConfirming('')}>
                        {t.downloads.cancelBtn}
                      </Button>
                    </div>
                  ) : (
                    <>
                      <Button
                        type="button"
                        variant="ghost"
                        aria-label={t.downloads.previewBtn}
                        title={t.downloads.previewBtn}
                        onClick={() => onPreview(f.name)}
                      >
                        <Eye className="w-3.5 h-3.5" />
                      </Button>
                      <Button
                        type="button"
                        variant="ghost"
                        aria-label={t.downloads.removeBtn}
                        title={t.downloads.removeBtn}
                        onClick={() => setConfirming(f.name)}
                      >
                        <Trash2 className="w-3.5 h-3.5" />
                      </Button>
                    </>
                  )}
                </li>
              );
            })}
          </ul>
        </section>
      )}
    </div>
  );
};
