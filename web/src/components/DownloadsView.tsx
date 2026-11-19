import React, { useState } from 'react';
import { Eye, FolderDown, FolderOpen, HardDriveDownload, Pause, Play, RefreshCw, Trash2, X } from 'lucide-react';
import type { DownloadItem, DownloadTask, Locale } from '../types';
import { formatBytes, inferFileCategory } from '../utils/fsLogic';
import { formatDateTime, formatPercent, type Translations } from '../utils/i18n';
import { RichFileTypeEmblem } from './VisualAssets';
import { Button } from './ui';

interface DownloadsViewProps {
  t: Translations;
  locale: Locale;
  dir: string;
  files: DownloadItem[];
  /** 进行中的下载任务；完成的任务会直接从列表里消失 */
  tasks: DownloadTask[];
  onRefresh: () => void;
  /** 下载目录动作：桌面用文件管理器打开，安卓把文件导出到系统下载目录 */
  dirAction: 'open' | 'export';
  onDirAction: () => void;
  onRemove: (name: string) => Promise<void> | void;
  onTask: (id: string, action: 'pause' | 'resume' | 'cancel') => void;
  /** 预览本地文件，不重新下载 */
  onPreview: (name: string) => void;
}

export const DownloadsView: React.FC<DownloadsViewProps> = (p) => {
  const { t, locale, dir, files, tasks, onRefresh, dirAction, onDirAction, onRemove, onTask, onPreview } = p;
  const [confirming, setConfirming] = useState('');
  const totalBytes = files.reduce((acc, f) => acc + f.size, 0);
  const percentOf = (task: DownloadTask) => (task.size > 0 ? Math.min(100, Math.floor((task.done / task.size) * 100)) : 0);

  return (
    <div className="stagger space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="space-y-1 min-w-0">
          <h1 className="text-xl sm:text-2xl font-semibold tracking-tight text-zinc-950 dark:text-white">{t.downloads.title}</h1>
          <p className="text-xs text-zinc-500 dark:text-zinc-400">
            {t.downloads.summary.replace('{count}', String(files.length)).replace('{size}', formatBytes(totalBytes))}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button type="button" variant="outline" onClick={onRefresh}>
            <RefreshCw className="w-3.5 h-3.5" />
            <span>{t.downloads.refreshBtn}</span>
          </Button>
          <Button type="button" variant="accent" onClick={onDirAction} data-testid="downloads-dir-action" data-dir-action={dirAction}>
            {dirAction === 'export' ? <FolderDown className="w-3.5 h-3.5" /> : <FolderOpen className="w-3.5 h-3.5" />}
            <span>{dirAction === 'export' ? t.downloads.exportDirBtn : t.downloads.openDirBtn}</span>
          </Button>
        </div>
      </div>

      {tasks.length > 0 && (
        <section className="surface-card rounded-2xl overflow-hidden">
          <h2 className="px-4 pt-3 pb-2 text-[11px] font-medium uppercase tracking-wide text-zinc-500 dark:text-zinc-400">
            {t.downloads.tasksTitle}
          </h2>
          <ul className="stagger divide-y divide-zinc-200/70 dark:divide-zinc-800/70">
            {tasks.map((task) => {
              const pct = percentOf(task);
              const failed = task.state === 'failed';
              return (
                <li key={task.id} className="px-4 py-3 space-y-2">
                  <div className="flex items-center gap-3">
                    <RichFileTypeEmblem category={inferFileCategory(task.name, false)} size="sm" />
                    <div className="min-w-0 flex-1">
                      <p className="text-sm font-medium text-zinc-900 dark:text-zinc-100 truncate" title={task.path}>
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
                        <Button type="button" variant="outline" size="icon" aria-label={t.downloads.taskPause} title={t.downloads.taskPause} onClick={() => onTask(task.id, 'pause')}>
                          <Pause className="w-3.5 h-3.5" />
                        </Button>
                      ) : (
                        <Button type="button" variant="outline" size="icon" aria-label={t.downloads.taskResume} title={t.downloads.taskResume} onClick={() => onTask(task.id, 'resume')}>
                          <Play className="w-3.5 h-3.5" />
                        </Button>
                      )}
                      <Button type="button" variant="ghost" size="icon" aria-label={t.downloads.taskCancel} title={t.downloads.taskCancel} onClick={() => onTask(task.id, 'cancel')}>
                        <X className="w-3.5 h-3.5" />
                      </Button>
                    </div>
                  </div>
                  {task.size > 0 && (
                    <div
                      role="progressbar"
                      aria-valuemin={0}
                      aria-valuemax={100}
                      aria-valuenow={Math.round(failed ? 100 : pct)}
                      aria-label={task.name}
                      className="h-1.5 w-full rounded-full bg-zinc-200/80 dark:bg-zinc-800 overflow-hidden"
                    >
                      <div
                        className={`h-full rounded-full transition-[width] duration-300 ${failed ? 'bg-[#FF3B30]' : 'bg-[#007AFF]'}`}
                        style={{ width: `${failed ? 100 : pct}%` }}
                      />
                    </div>
                  )}
                  {failed && task.error && <p className="text-[11px] text-red-600 dark:text-red-400 break-all">{task.error}</p>}
                </li>
              );
            })}
          </ul>
        </section>
      )}

      <section className="surface-card rounded-2xl px-4 py-3">
        <p className="text-[11px] text-zinc-500 dark:text-zinc-400">{t.downloads.dirLabel}</p>
        <p dir="ltr" className="mt-1 font-mono text-xs text-zinc-800 dark:text-zinc-200 break-all" title={dir}>
          {dir || '—'}
        </p>
      </section>

      {files.length === 0 ? (
        <section className="surface-card rounded-2xl p-10 text-center space-y-3">
          <div className="w-12 h-12 mx-auto rounded-2xl bg-zinc-100 dark:bg-zinc-800/70 inline-flex items-center justify-center text-zinc-400 dark:text-zinc-500">
            <HardDriveDownload className="w-6 h-6" />
          </div>
          <h2 className="text-sm font-semibold text-zinc-950 dark:text-white">{t.downloads.emptyTitle}</h2>
          <p className="text-xs text-zinc-500 dark:text-zinc-400 max-w-sm mx-auto">{t.downloads.emptyDesc}</p>
        </section>
      ) : (
        <section className="surface-card rounded-2xl overflow-hidden">
          <ul className="stagger divide-y divide-zinc-200/70 dark:divide-zinc-800/70">
            {files.map((f) => (
              <li key={f.name} data-download-name={f.name} className="flex items-center gap-3 px-4 py-3 hover:bg-zinc-50 dark:hover:bg-zinc-900/40 transition-colors">
                <RichFileTypeEmblem category={inferFileCategory(f.name, false)} size="sm" />
                <div className="min-w-0 flex-1">
                  <p className="text-sm font-medium text-zinc-900 dark:text-zinc-100 truncate" title={f.name}>
                    {f.name}
                  </p>
                  <p className="text-[11px] text-zinc-500 dark:text-zinc-400 font-mono tabular-nums">
                    {formatBytes(f.size)} · {formatDateTime(f.mod, locale)}
                  </p>
                </div>
                {confirming === f.name ? (
                  <div className="flex items-center gap-2 shrink-0">
                    <span className="text-[11px] text-red-600 dark:text-red-400">{t.downloads.confirmDelete}</span>
                    <Button
                      type="button"
                      variant="destructive"
                      autoFocus
                      onKeyDown={(e) => {
                        if (e.key !== 'Escape') return;
                        // 行内确认层的 Esc 自己消化，别让全局把焦点送回导航
                        e.preventDefault();
                        setConfirming('');
                      }}
                      onClick={() => {
                        setConfirming('');
                        void onRemove(f.name);
                      }}
                    >
                      {t.downloads.confirmDeleteBtn}
                    </Button>
                    <Button type="button" variant="outline" onClick={() => setConfirming('')}>
                      {t.downloads.cancelBtn}
                    </Button>
                  </div>
                ) : (
                  <>
                    <Button type="button" variant="ghost" aria-label={t.downloads.previewBtn} title={t.downloads.previewBtn} onClick={() => onPreview(f.name)}>
                      <Eye className="w-3.5 h-3.5" />
                    </Button>
                    <Button type="button" variant="ghost" aria-label={t.downloads.removeBtn} title={t.downloads.removeBtn} onClick={() => setConfirming(f.name)}>
                      <Trash2 className="w-3.5 h-3.5" />
                    </Button>
                  </>
                )}
              </li>
            ))}
          </ul>
        </section>
      )}
    </div>
  );
};
