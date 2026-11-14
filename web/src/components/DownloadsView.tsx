import React, { useState } from 'react';
import { FolderOpen, RefreshCw, Trash2, HardDriveDownload } from 'lucide-react';
import { DownloadItem, Locale } from '../types';
import { formatBytes, inferFileCategory } from '../utils/fsLogic';
import { Translations, formatDateTime } from '../utils/i18n';
import { RichFileTypeEmblem } from './VisualAssets';
import { Button } from './ui';

interface DownloadsViewProps {
  t: Translations;
  locale: Locale;
  dir: string;
  files: DownloadItem[];
  /** 刷新列表：服务端可能在别处新增或删除文件 */
  onRefresh: () => void;
  /** 用系统文件管理器打开下载目录 */
  onOpenDir: () => void;
  /** 删除本地副本；返回 Promise 以便页面自行收尾 */
  onRemove: (name: string) => Promise<void> | void;
}

// DownloadsView 展示服务端下载目录里的文件。
//
// 与浏览器下载不同，写入由服务端完成，因此页面只需刷新列表。
// 删除是本地副本操作，采用行内二次确认，避免误删。
export const DownloadsView: React.FC<DownloadsViewProps> = ({
  t,
  locale,
  dir,
  files,
  onRefresh,
  onOpenDir,
  onRemove,
}) => {
  const [confirming, setConfirming] = useState('');
  const totalBytes = files.reduce((acc, f) => acc + f.size, 0);

  const remove = (name: string) => {
    setConfirming('');
    void onRemove(name);
  };

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
                    <Button
                      type="button"
                      variant="ghost"
                      aria-label={t.downloads.removeBtn}
                      title={t.downloads.removeBtn}
                      onClick={() => setConfirming(f.name)}
                    >
                      <Trash2 className="w-3.5 h-3.5" />
                    </Button>
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
