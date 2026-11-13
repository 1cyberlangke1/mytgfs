import React from 'react';
import { CheckCircle2, DownloadCloud, Eye, HardDrive, MonitorDown, Trash2 } from 'lucide-react';
import { AppSettings, FileItem, Locale, OfflineSavedFile } from '../types';
import { formatBytes } from '../utils/fsLogic';
import { Translations, formatDateTime, interpolate } from '../utils/i18n';
import { usePWAInstall } from '../utils/usePWAInstall';
import { RichFileTypeEmblem } from './VisualAssets';
import { Button } from './ui';

interface OfflineViewProps {
  t: Translations;
  locale: Locale;
  settings: AppSettings;
  offlineFiles: OfflineSavedFile[];
  allFiles: FileItem[];
  onRemoveOfflineFile: (fileId: string) => void;
  onPreviewOfflineFile: (file: FileItem) => void;
}

export const OfflineView: React.FC<OfflineViewProps> = ({
  t,
  locale,
  settings,
  offlineFiles,
  allFiles,
  onRemoveOfflineFile,
  onPreviewOfflineFile,
}) => {
  const { isInstalled, isInstallable, isIOS, install } = usePWAInstall();

  const totalOfflineBytes = offlineFiles.reduce((acc, f) => acc + f.size, 0);

  return (
    <div className="space-y-6">
      {/* Header */}
      <h1 className="text-xl sm:text-2xl font-semibold tracking-tight text-zinc-950 dark:text-white">
        {t.offline.title}
      </h1>

      {/* Desktop PWA Installation Card */}
      <section className="surface-card rounded-xl p-5 flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <div className="flex items-start gap-3.5">
          <div className="w-10 h-10 rounded-lg bg-blue-500/10 text-blue-600 dark:text-blue-400 flex items-center justify-center shrink-0">
            <MonitorDown className="w-5 h-5" />
          </div>
          <div className="space-y-0.5">
            <h2 className="text-sm font-semibold text-zinc-950 dark:text-white">
              {t.offline.installCardTitle}
            </h2>
            <p className="text-xs text-zinc-500 dark:text-zinc-400">
              {t.offline.installCardDesc}
            </p>
            {isIOS && (
              <p className="text-xs text-amber-700 dark:text-amber-400 pt-1">
                {t.offline.iosGuideText}
              </p>
            )}
          </div>
        </div>

        <div className="flex items-center gap-2 shrink-0">
          {isInstalled ? (
            <div className="h-9 px-3 rounded-lg bg-emerald-500/10 border border-emerald-500/20 text-emerald-700 dark:text-emerald-300 text-xs font-medium inline-flex items-center gap-1.5">
              <CheckCircle2 className="w-3.5 h-3.5" />
              <span>{t.offline.installedBadge}</span>
            </div>
          ) : isInstallable ? (
            <Button type="button" variant="accent" onClick={install}>
              <DownloadCloud className="w-3.5 h-3.5" />
              <span>{t.offline.installBtn}</span>
            </Button>
          ) : (
            <span className="text-xs text-zinc-500 dark:text-zinc-400 max-w-[16rem] text-end">
              {t.offline.installUnavailable}
            </span>
          )}
        </div>
      </section>

      {/* Pinned Offline Files List */}
      <section className="space-y-3">
        <div className="flex items-center justify-between gap-2">
          <h2 className="text-sm font-semibold text-zinc-950 dark:text-white">
            {t.offline.savedListHeading}
          </h2>
          <span className="text-xs font-mono tabular-nums text-zinc-500 dark:text-zinc-400">
            <span dir="ltr">{formatBytes(totalOfflineBytes, 2, locale)}</span> ·{' '}
            {interpolate(
              t.offline.cacheQuotaLabel,
              { limit: settings.pwaOfflineMaxFileMb },
              locale
            )}
          </span>
        </div>

        {offlineFiles.length === 0 ? (
          <div className="surface-card rounded-xl p-10 text-center space-y-2">
            <HardDrive className="w-8 h-8 text-zinc-400 mx-auto" />
            <h3 className="text-sm font-medium text-zinc-900 dark:text-zinc-100">
              {t.offline.emptyOfflineTitle}
            </h3>
            <p className="text-xs text-zinc-500 dark:text-zinc-400 max-w-md mx-auto">
              {t.offline.emptyOfflineDesc}
            </p>
          </div>
        ) : (
          <div className="surface-card rounded-xl divide-y divide-zinc-200/80 dark:divide-zinc-800/80 overflow-hidden">
            {offlineFiles.map((item) => {
              const fullFile = allFiles.find((f) => f.id === item.fileId);
              return (
                <div
                  key={item.fileId}
                  className="p-4 flex flex-col sm:flex-row sm:items-center justify-between gap-4 hover:bg-zinc-50/60 dark:hover:bg-white/[0.015] transition-colors"
                >
                  <div className="flex items-center gap-3.5 min-w-0">
                    {item.thumbnailUrl ? (
                      <img
                        src={item.thumbnailUrl}
                        alt={item.name}
                        referrerPolicy="no-referrer"
                        className="w-11 h-9 rounded-lg object-cover border border-zinc-200 dark:border-zinc-800 shrink-0"
                      />
                    ) : (
                      <RichFileTypeEmblem category={item.category} size="sm" />
                    )}
                    <div className="min-w-0">
                      <p dir="ltr" className="text-sm font-medium text-zinc-900 dark:text-zinc-100 truncate">
                        {item.name}
                      </p>
                      <div className="flex items-center gap-2 text-xs font-mono tabular-nums text-zinc-500 dark:text-zinc-400 mt-0.5">
                        <span dir="ltr">{formatBytes(item.size, 2, locale)}</span>
                        <span aria-hidden="true">·</span>
                        <span>{formatDateTime(item.savedAt, locale)}</span>
                      </div>
                    </div>
                  </div>

                  <div className="flex items-center gap-2 shrink-0">
                    {fullFile && (
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        onClick={() => onPreviewOfflineFile(fullFile)}
                        aria-label={`${t.files.actionPreview} ${item.name}`}
                      >
                        <Eye className="w-3.5 h-3.5" />
                        <span>{t.files.actionPreview}</span>
                      </Button>
                    )}
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      onClick={() => onRemoveOfflineFile(item.fileId)}
                      aria-label={`${t.offline.removeOfflineBtn} ${item.name}`}
                      className="text-red-600 hover:text-red-700 hover:bg-red-500/10 dark:text-red-400 dark:hover:bg-red-500/10"
                    >
                      <Trash2 className="w-3.5 h-3.5" />
                      <span>{t.offline.removeOfflineBtn}</span>
                    </Button>
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </section>
    </div>
  );
};
