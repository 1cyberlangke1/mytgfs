import React, { useRef, useState } from 'react';
import {
  ArrowDownToLine,
  ArrowUpFromLine,
  ArrowUpRight,
  Camera,
  Database,
  FileStack,
  HardDrive,
  Pause,
  Play,
  RotateCw,
  X,
} from 'lucide-react';
import { AnimatePresence, motion } from 'motion/react';
import { coverURL } from '../api';
import type { FileCategory, FileItem, Locale, TransferTask, VolumeInfo } from '../types';
import {
  computeOverviewSummary,
  formatBytes,
  summarizeUploadBatch,
  volumeDisplayName,
} from '../utils/fsLogic';
import { formatNumber, formatPercent, formatPlural, interpolate, type Translations } from '../utils/i18n';
import { BrandEmblemSvg, RichFileTypeEmblem } from './VisualAssets';
import { CoverDialog } from './CoverDialog';
import { Button, IOS_SOFT_SPRING, IOS_SPRING } from './ui';

interface OverviewViewProps {
  t: Translations;
  locale: Locale;
  volumes: VolumeInfo[];
  files: FileItem[];
  transfers: TransferTask[];
  activeVolumeId: string;
  onSelectVolumeAndBrowse: (volumeId: string) => void;
  onUploadFiles: (fileList: FileList, volumeId: string, parentId: string | null) => void;
  onToggleTransferPause: (taskId: string) => void;
  onRetryTransfer: (taskId: string) => void;
  onCancelTransfer: (taskId: string) => void;
  onCancelQueueJob: (task: TransferTask) => void;
  onClearCompletedTransfers: () => void;
  /** 设置频道头像；nil 表示服务端不支持（面板隐藏入口） */
  onSetCover?: (volumeId: string, file: File) => Promise<void>;
  /** 改频道名字；没有提供时弹窗隐藏名字输入框 */
  onSetTitle?: (volumeId: string, title: string) => Promise<void>;
  /** 安卓：点封面走系统图片选择器（fd 直读，不复制到 cache） */
  onPickCoverNative?: (volumeId: string) => Promise<void>;
}

/** 卷内文件类型占比的颜色；顺序与分段条一致 */
const CATEGORY_COLORS: Record<FileCategory, string> = {
  archive: '#007AFF',
  video: '#F59E0B',
  audio: '#8B5CF6',
  image: '#10B981',
  pdf: '#EF4444',
  document: '#3B82F6',
  folder: '#64748B',
};

export const OverviewView: React.FC<OverviewViewProps> = (p) => {
  const { t, locale, volumes, files, transfers, activeVolumeId } = p;
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const [coverTarget, setCoverTarget] = useState<VolumeInfo | null>(null);
  const [coverBusy, setCoverBusy] = useState(false);

  const summary = computeOverviewSummary(volumes, files);
  const targetVolumeId = volumes.find((v) => v.id === activeVolumeId)?.id || volumes[0]?.id || '';
  const uploadBatch = summarizeUploadBatch(transfers);
  const uploadPct =
    uploadBatch && uploadBatch.total > 0 ? Math.min(100, Math.round((uploadBatch.done / uploadBatch.total) * 100)) : 0;

  const onFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    if (e.target.files && e.target.files.length > 0) {
      p.onUploadFiles(e.target.files, targetVolumeId, null);
      e.target.value = '';
    }
  };

  return (
    <div className="stagger space-y-6">
      <input
        ref={fileInputRef}
        type="file"
        multiple
        onChange={onFileChange}
        className="hidden"
        aria-label={t.overview.primaryUploadBtn}
      />

      <div className="flex items-center justify-between gap-4">
        <h1 className="text-xl sm:text-2xl font-semibold tracking-tight text-zinc-950 dark:text-white">
          {t.overview.title}
        </h1>
      </div>

      {/* 统计卡：卷数 / 文件数 / 总占用 */}
      <section className="stagger grid grid-cols-[repeat(auto-fit,minmax(150px,1fr))] gap-3.5 sm:gap-4">
        {(
          [
            ['summaryVolumes', summary.totalVolumes, 'summary-total-volumes', HardDrive, 'bg-[#007AFF]/10 text-[#007AFF] dark:text-[#0A84FF]'],
            ['summaryFiles', summary.totalFiles, 'summary-total-files', FileStack, 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400'],
            ['summaryTotalUsed', summary.totalBytes, 'summary-total-bytes', Database, 'bg-indigo-500/10 text-indigo-600 dark:text-indigo-400'],
          ] as const
        ).map(([key, value, testId, Icon, tone]) => (
          <motion.div
            key={testId}
            whileHover={{ y: -2 }}
            transition={IOS_SPRING}
            className="surface-card rounded-2xl p-4 sm:p-5 flex items-center justify-between gap-3"
          >
            <div className="space-y-1 min-w-0">
              <span className="text-xs font-medium text-zinc-500 dark:text-zinc-400 block truncate">{t.overview[key]}</span>
              <span
                dir={key === 'summaryTotalUsed' ? 'ltr' : undefined}
                data-testid={testId}
                className="text-xl lg:text-2xl font-semibold tabular-nums whitespace-nowrap text-zinc-950 dark:text-white block text-start"
              >
                {key === 'summaryTotalUsed' ? formatBytes(value, 2, locale) : formatNumber(value, locale)}
              </span>
            </div>
            <div className={`w-9 h-9 lg:w-10 lg:h-10 rounded-xl flex items-center justify-center shrink-0 ${tone}`}>
              <Icon className="w-4 h-4 lg:w-5 lg:h-5" />
            </div>
          </motion.div>
        ))}
      </section>

      {/* 卷卡片网格 */}
      <section className="space-y-3">
        <div className="flex items-center justify-between gap-4">
          <h2 className="text-base font-semibold text-zinc-950 dark:text-white">{t.overview.volumesHeading}</h2>
        </div>

        {summary.perVolume.length === 0 ? (
          <div className="surface-card rounded-2xl p-10 text-center space-y-3">
            <HardDrive className="w-8 h-8 text-zinc-400 mx-auto" />
            <div className="space-y-1">
              <h3 className="text-sm font-medium text-zinc-900 dark:text-zinc-100">{t.overview.emptyVolumesTitle}</h3>
              <p className="text-xs text-zinc-500 dark:text-zinc-400 max-w-md mx-auto">{t.overview.emptyVolumesDesc}</p>
            </div>
          </div>
        ) : (
          <div className="stagger grid grid-cols-1 min-[520px]:grid-cols-2 xl:grid-cols-3 gap-4">
            {summary.perVolume.map(({ volume, fileCount, folderCount, totalBytes, sharePct = 0 }) => {
              const isSelected = volume.id === activeVolumeId;
              // 卷内文件类型占比：分母是本卷总字节，只画有内容的类型
              const catTotals = (['archive', 'video', 'audio', 'image', 'pdf'] as FileCategory[])
                .map((cat) => {
                  const bytes = files
                    .filter((f) => f.volumeId === volume.id && !f.isDir && f.category === cat)
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
                    isSelected ? 'ring-2 ring-[#007AFF]/70 dark:ring-[#0A84FF]/60' : 'hover:border-zinc-300 dark:hover:border-zinc-700'
                  }`}
                >
                  <div>
                    <button
                      type="button"
                      disabled={!p.onSetCover}
                      onClick={() => p.onSetCover && setCoverTarget(volume)}
                      aria-label={p.onSetCover ? t.volume.coverDialogTitle : undefined}
                      className="relative block h-28 w-full overflow-hidden bg-gradient-to-br from-[#007AFF] via-[#0284C7] to-[#06B6D4] text-start disabled:cursor-default"
                    >
                      <BrandEmblemSvg className="absolute -end-4 -bottom-6 w-32 h-32 opacity-25" />
                      {/* 封面就是频道头像；加载失败保持品牌渐变 */}
                      {volume.cover && (
                        <img
                          src={coverURL(volume.id, volume.cover)}
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
                            {volumeDisplayName(volume.name, volume.title)}
                          </h3>
                        </div>
                        <span dir="ltr" className="text-xs font-mono font-semibold tabular-nums text-white/90 shrink-0">
                          {formatPercent(sharePct, locale)}
                        </span>
                      </div>
                      {p.onSetCover && (
                        <span className="absolute top-2 end-2 inline-flex items-center gap-1 rounded-lg bg-black/45 px-2 py-1 text-[11px] font-medium text-white opacity-0 group-hover:opacity-100 transition-opacity">
                          <Camera className="w-3 h-3" />
                          {t.volume.coverDialogTitle}
                        </span>
                      )}
                    </button>
                    <div className="px-4 pt-3.5 pb-1">
                      <div className="w-full h-1.5 rounded-full bg-zinc-200/80 dark:bg-zinc-800 overflow-hidden flex gap-0.5">
                        {catTotals.map((seg) => (
                          <div
                            key={seg.cat}
                            className="h-full first:rounded-s-full last:rounded-e-full"
                            style={{ width: `${Math.max(6, seg.pct)}%`, backgroundColor: CATEGORY_COLORS[seg.cat] }}
                          />
                        ))}
                      </div>
                    </div>
                  </div>
                  <div className="p-4 pt-3 flex items-center justify-between gap-3">
                    <div className="min-w-0">
                      <div dir="ltr" className="text-base font-semibold font-mono tabular-nums text-zinc-950 dark:text-white text-start">
                        {formatBytes(totalBytes, 1, locale)}
                      </div>
                      <div className="text-xs tabular-nums text-zinc-500 dark:text-zinc-400 truncate">
                        {formatPlural(fileCount, t.plurals.files, locale)} · {formatPlural(folderCount, t.plurals.folders, locale)}
                      </div>
                    </div>
                    <Button type="button" variant="secondary" size="sm" onClick={() => p.onSelectVolumeAndBrowse(volume.id)}>
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

      {/* 最近传输 */}
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
            <Button type="button" variant="ghost" size="sm" onClick={p.onClearCompletedTransfers}>
              {t.overview.clearCompletedBtn}
            </Button>
          )}
        </div>

        {uploadBatch && (
          <div className="surface-card rounded-2xl px-4 py-3 space-y-2">
            <div className="flex items-center justify-between gap-3 text-xs">
              <span className="font-medium text-zinc-800 dark:text-zinc-200">
                {interpolate(t.overview.batchUploadLabel, { count: uploadBatch.files }, locale)}
              </span>
              <span className="font-mono tabular-nums text-zinc-500 dark:text-zinc-400">
                {formatPercent(uploadPct, locale)} · {formatBytes(uploadBatch.done, 2, locale)} / {formatBytes(uploadBatch.total, 2, locale)}
              </span>
            </div>
            <div
              role="progressbar"
              aria-valuemin={0}
              aria-valuemax={100}
              aria-valuenow={uploadPct}
              aria-label={interpolate(t.overview.batchUploadLabel, { count: uploadBatch.files }, locale)}
              className="h-1.5 rounded-full bg-zinc-200 dark:bg-zinc-800 overflow-hidden"
            >
              <div className="h-full rounded-full bg-[#007AFF] dark:bg-[#0A84FF] transition-[width] duration-300" style={{ width: `${uploadPct}%` }} />
            </div>
          </div>
        )}

        {transfers.length === 0 ? (
          <div className="surface-card rounded-2xl p-8 text-center">
            <p className="text-sm font-medium text-zinc-800 dark:text-zinc-200">{t.overview.emptyTransfersTitle}</p>
          </div>
        ) : (
          <div className="surface-card rounded-2xl divide-y divide-zinc-200/80 dark:divide-zinc-800/80 overflow-hidden">
            <AnimatePresence initial={false}>
              {transfers.map((task) => {
                const unknownSize = task.size === 0;
                const pct = unknownSize ? 0 : Math.min(100, Math.round((task.transferredBytes / task.size) * 100));
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
                        : task.state === 'cancelled'
                          ? t.overview.transferCancelled
                          : task.state === 'completed'
                            ? t.overview.transferCompleted
                            : t.overview.transferFailed;
                const isQueue = task.id.startsWith('q-');

                return (
                  <motion.div
                    key={task.id}
                    layout
                    data-queue-state={task.state}
                    data-queue-kind={opKind}
                    initial={{ opacity: 0, y: 8 }}
                    animate={{ opacity: 1, y: 0 }}
                    exit={{ opacity: 0, height: 0 }}
                    transition={IOS_SOFT_SPRING}
                    className="p-4 flex flex-col sm:flex-row sm:items-center justify-between gap-4 hover:bg-zinc-50/60 dark:hover:bg-white/[0.015] transition-colors"
                  >
                    <div className="flex items-start gap-3 min-w-0 flex-1">
                      <RichFileTypeEmblem category={task.category || 'archive'} size="sm" />
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
                          {task.direction === 'upload' && task.phase && (
                            <span className="px-2 py-0.5 rounded-full bg-zinc-100 dark:bg-zinc-800 text-[10px] font-sans font-medium text-zinc-600 dark:text-zinc-300">
                              {task.phase === 'local' ? t.overview.phaseLocal : t.overview.phaseRemote}
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
                                    : task.state === 'cancelled'
                                      ? 'text-zinc-500 dark:text-zinc-400'
                                      : ''
                            }
                          >
                            {stateLabel}
                          </span>
                          <span aria-hidden="true">·</span>
                          <span dir="ltr">
                            {unknownSize
                              ? '—'
                              : `${formatBytes(task.transferredBytes, 2, locale)} / ${formatBytes(task.size, 2, locale)} (${formatPercent(pct, locale)})`}
                          </span>
                          {task.isChunkedResume && (
                            <>
                              <span aria-hidden="true">·</span>
                              <span>
                                {t.overview.chunkedResumeTag} ({formatNumber(task.currentChunk, locale)}/{formatNumber(task.totalChunks, locale)})
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
                          <p className="text-[11px] leading-relaxed text-red-600 dark:text-red-400 break-words">{task.errorMessage}</p>
                        )}

                        {!unknownSize && (
                          <div
                            role="progressbar"
                            aria-valuemin={0}
                            aria-valuemax={100}
                            aria-valuenow={Math.round(pct)}
                            aria-label={task.fileName}
                            className="w-full h-1.5 rounded-full bg-zinc-200/80 dark:bg-zinc-800 overflow-hidden"
                          >
                            <div
                              className={`h-full transition-transform duration-200 origin-left ${
                                task.state === 'completed'
                                  ? 'bg-emerald-500'
                                  : task.state === 'failed'
                                    ? 'bg-red-500'
                                    : task.state === 'paused'
                                      ? 'bg-amber-500'
                                      : task.state === 'cancelled'
                                        ? 'bg-zinc-400 dark:bg-zinc-600'
                                        : 'bg-[#007AFF] dark:bg-[#0A84FF]'
                              }`}
                              style={{ transform: `scaleX(${pct / 100})` }}
                            />
                          </div>
                        )}
                      </div>
                    </div>

                    <div className="flex items-center gap-1.5 shrink-0">
                      {!isQueue && task.direction === 'upload' && task.state === 'failed' && (
                        <Button type="button" variant="outline" size="sm" onClick={() => p.onRetryTransfer(task.id)} aria-label={`${t.overview.retryBtn} ${task.fileName}`}>
                          <RotateCw className="w-3.5 h-3.5" />
                          <span>{t.overview.retryBtn}</span>
                        </Button>
                      )}
                      {!isQueue && task.direction === 'upload' && (task.state === 'transferring' || task.state === 'paused' || task.state === 'queued') && (
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          onClick={() => p.onToggleTransferPause(task.id)}
                          aria-label={`${task.state === 'transferring' ? t.overview.pauseBtn : t.overview.resumeBtn} ${task.fileName}`}
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
                      {/* 服务端队列任务：排队中/执行中给「取消」；本地任务只有上传能取消 */}
                      {isQueue
                        ? (task.state === 'queued' || task.state === 'transferring') && (
                            <Button
                              type="button"
                              variant="ghost"
                              size="icon"
                              onClick={() => p.onCancelQueueJob(task)}
                              data-testid="queue-cancel"
                              aria-label={`${t.overview.cancelBtn} ${task.fileName}`}
                              className="text-zinc-400 hover:text-red-600 dark:hover:text-red-400"
                            >
                              <X className="w-4 h-4" />
                            </Button>
                          )
                        : task.direction !== 'download' && (
                            <Button
                              type="button"
                              variant="ghost"
                              size="icon"
                              onClick={() => p.onCancelTransfer(task.id)}
                              aria-label={`${t.overview.cancelBtn} ${task.fileName}`}
                              className="text-zinc-400 hover:text-red-600 dark:hover:text-red-400"
                            >
                              <X className="w-4 h-4" />
                            </Button>
                          )}
                    </div>
                  </motion.div>
                );
              })}
            </AnimatePresence>
          </div>
        )}
      </section>

      <CoverDialog
        open={coverTarget !== null}
        volumeName={coverTarget?.name ?? ''}
        currentTitle={coverTarget?.title ?? ''}
        t={t}
        busy={coverBusy}
        onClose={() => {
          if (!coverBusy) setCoverTarget(null);
        }}
        nativePicker={!!p.onPickCoverNative}
        onPickNative={async () => {
          if (!coverTarget || !p.onPickCoverNative) return;
          setCoverBusy(true);
          try {
            await p.onPickCoverNative(coverTarget.id);
            setCoverTarget(null);
          } finally {
            setCoverBusy(false);
          }
        }}
        onSubmitTitle={async (title) => {
          if (!coverTarget || !p.onSetTitle) return;
          setCoverBusy(true);
          try {
            await p.onSetTitle(coverTarget.id, title);
          } finally {
            setCoverBusy(false);
          }
        }}
        onSubmit={async (file) => {
          if (!coverTarget || !p.onSetCover) return;
          setCoverBusy(true);
          try {
            await p.onSetCover(coverTarget.id, file);
            setCoverTarget(null);
          } finally {
            setCoverBusy(false);
          }
        }}
      />
    </div>
  );
};
