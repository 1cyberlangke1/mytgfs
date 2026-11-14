import React, { useRef } from 'react';
import {
  AlertTriangle,
  ArrowDownToLine,
  ArrowUpFromLine,
  ArrowUpRight,
  CheckCircle2,
  Database,
  FileStack,
  HardDrive,
  Loader2,
  Pause,
  Play,
  X,
} from 'lucide-react';
import { AnimatePresence, motion } from 'motion/react';
import {
  ConnectionStatus,
  FileCategory,
  FileItem,
  Locale,
  TransferTask,
  VolumeInfo,
} from '../types';
import { computeOverviewSummary, formatBytes } from '../utils/fsLogic';
import { coverURL } from '../api';
import {
  Translations,
  formatNumber,
  formatPercent,
  formatPlural,
} from '../utils/i18n';
import { BrandEmblemSvg, RichFileTypeEmblem } from './VisualAssets';
import { Button, IOS_SOFT_SPRING, IOS_SPRING } from './ui';

interface OverviewViewProps {
  t: Translations;
  locale: Locale;
  connectionStatus: ConnectionStatus;
  volumes: VolumeInfo[];
  files: FileItem[];
  transfers: TransferTask[];
  activeVolumeId: string;
  onSelectVolumeAndBrowse: (volumeId: string) => void;
  onUploadFiles: (fileList: FileList, volumeId: string, parentId: string | null) => void;
  onToggleTransferPause: (taskId: string) => void;
  onCancelTransfer: (taskId: string) => void;
  onClearCompletedTransfers: () => void;
  /** 封面版本号：改完封面自增，绕过浏览器缓存 */
  coverVersion: number;
}


const CATEGORY_COLORS: Record<FileCategory, string> = {
  archive: '#007AFF',
  video: '#F59E0B',
  audio: '#8B5CF6',
  image: '#10B981',
  pdf: '#EF4444',
  document: '#3B82F6',
  folder: '#64748B',
};

export const OverviewView: React.FC<OverviewViewProps> = ({
  t,
  locale,
  connectionStatus,
  volumes,
  files,
  transfers,
  activeVolumeId,
  onSelectVolumeAndBrowse,
  onUploadFiles,
  onToggleTransferPause,
  onCancelTransfer,
  onClearCompletedTransfers,
  coverVersion,
}) => {
  const fileInputRef = useRef<HTMLInputElement | null>(null);

  const summary = computeOverviewSummary(volumes, files);
  const targetVolumeId =
    volumes.find((v) => v.id === activeVolumeId)?.id || volumes[0]?.id || '';

  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    if (e.target.files && e.target.files.length > 0) {
      onUploadFiles(e.target.files, targetVolumeId, null);
      e.target.value = '';
    }
  };

  const renderConnectionIcon = () => {
    if (connectionStatus === 'connected') {
      return <CheckCircle2 className="w-4 h-4 text-emerald-600 dark:text-emerald-400 shrink-0" />;
    }
    if (connectionStatus === 'connecting' || connectionStatus === 'reconnecting') {
      return <Loader2 className="w-4 h-4 text-amber-600 dark:text-amber-400 animate-spin shrink-0" />;
    }
    return <AlertTriangle className="w-4 h-4 text-red-600 dark:text-red-400 shrink-0" />;
  };

  const connectionDescMap: Record<ConnectionStatus, string> = {
    connected: t.connection.desc_connected,
    connecting: t.connection.desc_connecting,
    reconnecting: t.connection.desc_reconnecting,
    invalid_credentials: t.connection.desc_invalid_credentials,
  };

  return (
    <div className="space-y-6">
      <input
        ref={fileInputRef}
        type="file"
        multiple
        onChange={handleFileChange}
        className="hidden"
        aria-label={t.overview.primaryUploadBtn}
      />

      {/* Page Title */}
      <div className="flex items-center justify-between gap-4">
        <h1 className="text-xl sm:text-2xl font-semibold tracking-tight text-zinc-950 dark:text-white">
          {t.overview.title}
        </h1>
      </div>

      {/* 3-Card Summary Row */}
      <section className="grid grid-cols-1 sm:grid-cols-3 gap-3.5 sm:gap-4">
        <motion.div
          whileHover={{ y: -2 }}
          transition={IOS_SPRING}
          className="surface-card rounded-2xl p-4 sm:p-5 flex items-center justify-between gap-3"
        >
          <div className="space-y-1 min-w-0">
            <span className="text-xs font-medium text-zinc-500 dark:text-zinc-400 block truncate">
              {t.overview.summaryVolumes}
            </span>
            <span
              data-testid="summary-total-volumes"
              className="text-xl lg:text-2xl font-semibold tabular-nums whitespace-nowrap text-zinc-950 dark:text-white block"
            >
              {formatNumber(summary.totalVolumes, locale)}
            </span>
          </div>
          <div className="w-9 h-9 lg:w-10 lg:h-10 rounded-xl bg-[#007AFF]/10 text-[#007AFF] dark:text-[#0A84FF] flex items-center justify-center shrink-0">
            <HardDrive className="w-4 h-4 lg:w-5 lg:h-5" />
          </div>
        </motion.div>

        <motion.div
          whileHover={{ y: -2 }}
          transition={IOS_SPRING}
          className="surface-card rounded-2xl p-4 sm:p-5 flex items-center justify-between gap-3"
        >
          <div className="space-y-1 min-w-0">
            <span className="text-xs font-medium text-zinc-500 dark:text-zinc-400 block truncate">
              {t.overview.summaryFiles}
            </span>
            <span
              data-testid="summary-total-files"
              className="text-xl lg:text-2xl font-semibold tabular-nums whitespace-nowrap text-zinc-950 dark:text-white block"
            >
              {formatNumber(summary.totalFiles, locale)}
            </span>
          </div>
          <div className="w-9 h-9 lg:w-10 lg:h-10 rounded-xl bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 flex items-center justify-center shrink-0">
            <FileStack className="w-4 h-4 lg:w-5 lg:h-5" />
          </div>
        </motion.div>

        <motion.div
          whileHover={{ y: -2 }}
          transition={IOS_SPRING}
          className="surface-card rounded-2xl p-4 sm:p-5 flex items-center justify-between gap-3"
        >
          <div className="space-y-1 min-w-0">
            <span className="text-xs font-medium text-zinc-500 dark:text-zinc-400 block truncate">
              {t.overview.summaryTotalUsed}
            </span>
            <span
              dir="ltr"
              data-testid="summary-total-bytes"
              className="text-xl lg:text-2xl font-semibold tabular-nums whitespace-nowrap text-zinc-950 dark:text-white block text-start"
            >
              {formatBytes(summary.totalBytes, 2, locale)}
            </span>
          </div>
          <div className="w-9 h-9 lg:w-10 lg:h-10 rounded-xl bg-indigo-500/10 text-indigo-600 dark:text-indigo-400 flex items-center justify-center shrink-0">
            <Database className="w-4 h-4 lg:w-5 lg:h-5" />
          </div>
        </motion.div>
      </section>

      {/* Connection Status & Index Bar with iOS Segmented Control */}
      <section className="surface-card rounded-2xl overflow-hidden">
        <div className="px-5 py-3.5 flex flex-col lg:flex-row lg:items-center justify-between gap-4">
          <div className="flex items-start sm:items-center gap-3">
            {renderConnectionIcon()}
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2 text-xs">
                <span className="font-semibold text-zinc-900 dark:text-zinc-100">
                  {t.connection.label}: {t.connection[connectionStatus]}
                </span>
              </div>
              {connectionStatus !== 'connected' && (
                <p className="text-xs text-amber-600 dark:text-amber-400 mt-0.5">
                  {connectionDescMap[connectionStatus]}
                </p>
              )}
            </div>
          </div>

          <div className="flex flex-wrap items-center gap-2">
            {/* 连接状态由服务端给出，这里只展示，不可手动切换 */}
            <span className="inline-flex items-center gap-2 rounded-full border border-zinc-200/80 dark:border-zinc-800/80 bg-zinc-50/60 dark:bg-zinc-900/40 px-3 py-1.5 text-xs font-medium text-zinc-700 dark:text-zinc-300">
              <CheckCircle2 className="w-3.5 h-3.5 text-emerald-500" />
              {t.connection[connectionStatus]}
            </span>
          </div>
        </div>
      </section>

      {/* Volume List */}
      <section className="space-y-3">
        <div className="flex items-center justify-between gap-4">
          <h2 className="text-base font-semibold text-zinc-950 dark:text-white">
            {t.overview.volumesHeading}
          </h2>
        </div>

        {volumes.length === 0 ? (
          <div className="surface-card rounded-2xl p-10 text-center space-y-3">
            <HardDrive className="w-8 h-8 text-zinc-400 mx-auto" />
            <div className="space-y-1">
              <h3 className="text-sm font-medium text-zinc-900 dark:text-zinc-100">
                {t.overview.emptyVolumesTitle}
              </h3>
              <p className="text-xs text-zinc-500 dark:text-zinc-400 max-w-md mx-auto">
                {t.overview.emptyVolumesDesc}
              </p>
            </div>
          </div>
        ) : (
          <div className="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-3 gap-4">
            {summary.perVolume.map(({ volume, fileCount, folderCount, totalBytes, sharePct }) => {
              const isSelected = volume.id === activeVolumeId;

              // 卷间占比来自 summary（最大余数法），文件类型占比留在卷内

              // Compute file-type composition segments inside this volume
              const volFiles = files.filter((f) => f.volumeId === volume.id && !f.isDir);
              const catTotals = (['archive', 'video', 'audio', 'image', 'pdf'] as FileCategory[])
                .map((cat) => {
                  const bytes = volFiles
                    .filter((f) => f.category === cat)
                    .reduce((acc, f) => acc + f.size, 0);
                  return { cat, bytes, pct: (bytes / Math.max(1, totalBytes)) * 100 };
                })
                .filter((seg) => seg.bytes > 0);

              return (
                <motion.div
                  key={volume.id}
                  whileHover={{ y: -3 }}
                  transition={IOS_SPRING}
                  className={`surface-card rounded-2xl overflow-hidden flex flex-col justify-between transition-shadow group ${
                    isSelected
                      ? 'ring-2 ring-[#007AFF]/70 dark:ring-[#0A84FF]/60'
                      : 'hover:border-zinc-300 dark:hover:border-zinc-700'
                  }`}
                >
                  <div>
                    <div className="relative h-28 w-full overflow-hidden bg-gradient-to-br from-[#007AFF] via-[#0284C7] to-[#06B6D4]">
                      <BrandEmblemSvg className="absolute -end-4 -bottom-6 w-32 h-32 opacity-25" />
                      {/* 卷根目录存在 .cover.jpg / cover.png 时显示自定义封面，加载失败保持渐变 */}
                      {volume.hasCover && (
                        <img
                          src={`${coverURL(volume.id)}&v=${coverVersion}`}
                          alt=""
                          aria-hidden="true"
                          loading="lazy"
                          onError={(ev) => {
                            ev.currentTarget.style.display = 'none';
                          }}
                          className="absolute inset-0 w-full h-full object-cover group-hover:scale-105 transition-transform duration-500"
                        />
                      )}
                      <div className="absolute inset-0 bg-gradient-to-t from-black/85 via-black/40 to-black/10" />

                      <div className="absolute bottom-3 inset-x-4 flex items-end justify-between gap-2">
                        <div className="min-w-0">
                          <h3 dir="ltr" className="text-base font-semibold text-white font-mono truncate">
                            {volume.name}
                          </h3>
                        </div>
                        <span dir="ltr" className="text-xs font-mono font-semibold tabular-nums text-white/90 shrink-0">
                          {formatPercent(sharePct, locale)}
                        </span>
                      </div>
                    </div>

                    <div className="px-4 pt-3.5 pb-1">
                      <div className="w-full h-1.5 rounded-full bg-zinc-200/80 dark:bg-zinc-800 overflow-hidden flex gap-0.5">
                        {catTotals.map((seg) => (
                          <div
                            key={seg.cat}
                            className="h-full first:rounded-s-full last:rounded-e-full"
                            style={{
                              width: `${Math.max(6, seg.pct)}%`,
                              backgroundColor: CATEGORY_COLORS[seg.cat],
                            }}
                          />
                        ))}
                      </div>
                    </div>
                  </div>

                  <div className="p-4 pt-3 flex items-center justify-between gap-3">
                    <div className="min-w-0">
                      <div
                        dir="ltr"
                        className="text-base font-semibold font-mono tabular-nums text-zinc-950 dark:text-white text-start"
                      >
                        {formatBytes(totalBytes, 1, locale)}
                      </div>
                      <div className="text-xs tabular-nums text-zinc-500 dark:text-zinc-400 truncate">
                        {formatPlural(fileCount, t.plurals.files, locale)} ·{' '}
                        {formatPlural(folderCount, t.plurals.folders, locale)}
                      </div>
                    </div>

                    <Button
                      type="button"
                      variant="secondary"
                      size="sm"
                      onClick={() => onSelectVolumeAndBrowse(volume.id)}
                    >
                      <span>{t.overview.browseVolumeBtn}</span>
                      <ArrowUpRight className="w-3.5 h-3.5" />
                    </Button>
                  </div>
                </motion.div>
              );
            })}
          </div>
        )}
      </section>

      {/* Recent Transfers */}
      <section className="space-y-3">
        <div className="flex items-center justify-between gap-4">
          <h2 className="text-base font-semibold text-zinc-950 dark:text-white">
            {t.overview.recentTransfersHeading}
            {transfers.length > 0 && (
              <span className="ms-2 text-xs font-normal text-zinc-500 dark:text-zinc-400 tabular-nums">
                {formatNumber(transfers.length, locale)}
              </span>
            )}
          </h2>
          {transfers.some((tr) => tr.state === 'completed' || tr.state === 'failed') && (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={onClearCompletedTransfers}
            >
              {t.overview.clearCompletedBtn}
            </Button>
          )}
        </div>

        {transfers.length === 0 ? (
          <div className="surface-card rounded-2xl p-8 text-center">
            <p className="text-sm font-medium text-zinc-800 dark:text-zinc-200">
              {t.overview.emptyTransfersTitle}
            </p>
          </div>
        ) : (
          <div className="surface-card rounded-2xl divide-y divide-zinc-200/80 dark:divide-zinc-800/80 overflow-hidden">
            <AnimatePresence initial={false}>
              {transfers.map((task) => {
                const unknownSize = task.size === 0;
                const pct = unknownSize
                  ? 0
                  : Math.min(100, Math.round((task.transferredBytes / task.size) * 100));
                const opKind = task.opKind ?? task.direction;
                const kindLabel = t.overview.queueKind[opKind] ?? '';
                const stateLabel =
                  task.state === 'transferring'
                    ? opKind === 'download'
                      ? t.overview.transferDownloading
                      : opKind === 'upload'
                      ? t.overview.transferUploading
                      : t.overview.queueRunning
                    : task.state === 'queued'
                    ? t.overview.transferQueued
                    : task.state === 'paused'
                    ? t.overview.transferPaused
                    : task.state === 'completed'
                    ? t.overview.transferCompleted
                    : t.overview.transferFailed;

                return (
                  <motion.div
                    key={task.id}
                    layout
                    initial={{ opacity: 0, y: 8 }}
                    animate={{ opacity: 1, y: 0 }}
                    exit={{ opacity: 0, height: 0 }}
                    transition={IOS_SOFT_SPRING}
                    className="p-4 flex flex-col sm:flex-row sm:items-center justify-between gap-4 hover:bg-zinc-50/60 dark:hover:bg-white/[0.015] transition-colors"
                  >
                    <div className="flex items-start gap-3 min-w-0 flex-1">
                      <RichFileTypeEmblem
                        category={task.category || 'archive'}
                        size="sm"
                      />

                      <div className="min-w-0 flex-1 space-y-1.5">
                        <div className="flex items-center gap-2">
                          {task.direction === 'upload' ? (
                            <ArrowUpFromLine className="w-3.5 h-3.5 text-[#007AFF] dark:text-[#0A84FF] shrink-0" />
                          ) : (
                            <ArrowDownToLine className="w-3.5 h-3.5 text-emerald-600 dark:text-emerald-400 shrink-0" />
                          )}
                          <span dir="ltr" className="text-sm font-medium text-zinc-900 dark:text-zinc-100 truncate">
                            {task.fileName}
                          </span>
                        </div>

                        <div className="flex flex-wrap items-center gap-2 text-xs font-mono tabular-nums text-zinc-500 dark:text-zinc-400">
                          {kindLabel && (
                            <span className="px-2 py-0.5 rounded-full bg-zinc-100 dark:bg-zinc-800 text-[10px] font-sans font-medium text-zinc-600 dark:text-zinc-300">
                              {kindLabel}
                            </span>
                          )}
                          <span
                            className={
                              task.state === 'completed'
                                ? 'text-emerald-600 dark:text-emerald-400 font-medium'
                                : task.state === 'failed'
                                ? 'text-red-600 dark:text-red-400 font-medium'
                                : task.state === 'transferring'
                                ? 'text-[#007AFF] dark:text-[#0A84FF] font-medium'
                                : ''
                            }
                          >
                            {stateLabel}
                          </span>
                          <span aria-hidden="true">·</span>
                          <span dir="ltr">
                            {unknownSize
                              ? '—'
                              : `${formatBytes(task.transferredBytes, 2, locale)} / ${formatBytes(
                                  task.size,
                                  2,
                                  locale
                                )} (${formatPercent(pct, locale)})`}
                          </span>
                          {task.isChunkedResume && (
                            <>
                              <span aria-hidden="true">·</span>
                              <span>
                                {t.overview.chunkedResumeTag} ({formatNumber(task.currentChunk, locale)}/
                                {formatNumber(task.totalChunks, locale)})
                              </span>
                            </>
                          )}
                          {task.state === 'transferring' && task.speedBps > 0 && (
                            <>
                              <span aria-hidden="true">·</span>
                              <span dir="ltr">{formatBytes(task.speedBps, 1, locale)}/s</span>
                            </>
                          )}
                        </div>

                        {/* 失败原因必须留在界面上，否则用户只看到「失败」不知道怎么办 */}
                        {task.state === 'failed' && task.errorMessage && (
                          <p className="text-[11px] leading-relaxed text-red-600 dark:text-red-400 break-words">
                            {task.errorMessage}
                          </p>
                        )}

                        {!unknownSize && (
                          <div className="w-full h-1.5 rounded-full bg-zinc-200/80 dark:bg-zinc-800 overflow-hidden">
                            <div
                              className={`h-full transition-transform duration-200 origin-left ${
                                task.state === 'completed'
                                  ? 'bg-emerald-500'
                                  : task.state === 'failed'
                                  ? 'bg-red-500'
                                  : task.state === 'paused'
                                  ? 'bg-amber-500'
                                  : 'bg-[#007AFF] dark:bg-[#0A84FF]'
                              }`}
                              style={{ transform: `scaleX(${pct / 100})` }}
                            />
                          </div>
                        )}
                      </div>
                    </div>

                    <div className="flex items-center gap-1.5 shrink-0">
                      {!task.id.startsWith('q-') &&
                        task.direction === 'upload' &&
                        (task.state === 'transferring' ||
                          task.state === 'paused' ||
                          task.state === 'queued') && (
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          onClick={() => onToggleTransferPause(task.id)}
                          aria-label={`${
                            task.state === 'transferring'
                              ? t.overview.pauseBtn
                              : t.overview.resumeBtn
                          } ${task.fileName}`}
                        >
                          {task.state === 'transferring' ? (
                            <>
                              <Pause className="w-3.5 h-3.5" />
                              <span>{t.overview.pauseBtn}</span>
                            </>
                          ) : (
                            <>
                              <Play className="w-3.5 h-3.5" />
                              <span>{t.overview.resumeBtn}</span>
                            </>
                          )}
                        </Button>
                      )}
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        onClick={() => onCancelTransfer(task.id)}
                        aria-label={`${t.overview.cancelBtn} ${task.fileName}`}
                        className={`text-zinc-400 hover:text-red-600 dark:hover:text-red-400 ${
                          task.direction === 'download' || task.id.startsWith('q-') ? 'hidden' : ''
                        }`}
                      >
                        <X className="w-4 h-4" />
                      </Button>
                    </div>
                  </motion.div>
                );
              })}
            </AnimatePresence>
          </div>
        )}
      </section>
    </div>
  );
};
