import React, { useEffect, useState } from 'react';
import {
  Download,
  Folder,
  HardDrive,
  Image as ImageIcon,
  Maximize2,
  Minimize2,
  Music,
  X,
} from 'lucide-react';
import { AnimatePresence, motion } from 'motion/react';
import { FileItem, Locale } from '../types';
import { fileURL } from '../api';
import { formatBytes, getItemFullPath } from '../utils/fsLogic';
import { Translations, formatDateTime, formatNumber } from '../utils/i18n';
import { Button, IOS_SPRING } from './ui';

interface FilePreviewModalProps {
  file: FileItem | null;
  allFiles: FileItem[];
  t: Translations;
  locale: Locale;
  onClose: () => void;
  onDownload: (file: FileItem) => void;
  onSaveOffline: (file: FileItem) => void;
  isOfflineSaved: boolean;
}

export const FilePreviewModal: React.FC<FilePreviewModalProps> = ({
  file,
  allFiles,
  t,
  locale,
  onClose,
  onDownload,
  onSaveOffline,
  isOfflineSaved,
}) => {
  const [imgZoomed, setImgZoomed] = useState(false);
  const [imgFailed, setImgFailed] = useState(false);

  useEffect(() => {
    setImgZoomed(false);
    setImgFailed(false);
  }, [file?.id]);

  // Keyboard Escape to close
  useEffect(() => {
    if (!file) return;
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', handleKey);
    return () => window.removeEventListener('keydown', handleKey);
  }, [file, onClose]);

  return (
    <AnimatePresence>
      {file && (
        <motion.div
          initial={{ opacity: 0 }}
          animate={{ opacity: 1 }}
          exit={{ opacity: 0, transition: { duration: 0.14 } }}
          className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/70 backdrop-blur-xs p-4 overflow-y-auto"
          role="dialog"
          aria-modal="true"
          aria-labelledby="file-detail-modal-title"
        >
          <motion.div
            initial={{ opacity: 0, scale: 0.94, y: 12 }}
            animate={{ opacity: 1, scale: 1, y: 0 }}
            exit={{ opacity: 0, scale: 0.95, y: 8, transition: { duration: 0.14 } }}
            transition={IOS_SPRING}
            className="w-full max-w-4xl rounded-2xl bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 shadow-xl overflow-hidden my-auto"
          >
            {(() => {
              const fullPath = getItemFullPath(allFiles, file);
              // 真实媒体流地址：服务端 /api/file 支持 Range，可直接喂给 video/audio/iframe
              const streamUrl = fileURL(file.volumeId, fullPath, true);

              return (
                <>
                  {/* Header */}
                  <div className="flex items-center justify-between gap-4 px-6 py-4 border-b border-slate-200 dark:border-slate-800">
                    <div className="min-w-0">
                      <h2
                        id="file-detail-modal-title"
                        dir="ltr"
                        className="text-base font-semibold text-slate-900 dark:text-slate-100 truncate text-start"
                      >
                        {file.name}
                      </h2>
                      <p className="text-xs text-slate-500 dark:text-slate-400 font-mono tabular-nums truncate mt-0.5">
                        {t.files.detailTitle} · <span dir="ltr">{formatBytes(file.size, 2, locale)}</span> ·{' '}
                        <span dir="ltr">{fullPath}</span>
                      </p>
                    </div>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      onClick={onClose}
                      aria-label={t.files.closeDetail}
                    >
                      <X className="w-5 h-5" />
                    </Button>
                  </div>

                  <div className="grid grid-cols-1 lg:grid-cols-12">
                    {/* Left/Top: Media Preview Viewport */}
                    <div className="lg:col-span-8 p-6 bg-slate-50 dark:bg-slate-950/60 border-b lg:border-b-0 lg:border-e border-slate-200 dark:border-slate-800 flex flex-col justify-between min-h-[340px]">
                      <div className="flex items-center justify-between mb-3">
                        <span className="text-xs font-medium text-slate-500 dark:text-slate-400">
                          {t.files.detailPreviewHeading}
                        </span>
                        {file.category === 'image' && (
                          <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            onClick={() => setImgZoomed((z) => !z)}
                          >
                            {imgZoomed ? (
                              <Minimize2 className="w-3.5 h-3.5" />
                            ) : (
                              <Maximize2 className="w-3.5 h-3.5" />
                            )}
                            <span>{imgZoomed ? '100%' : t.files.detailZoom}</span>
                          </Button>
                        )}
                      </div>

                      {/* IMAGE PREVIEW */}
                      {file.category === 'image' && (
                        <div className="relative flex-1 flex items-center justify-center rounded-lg overflow-hidden bg-slate-900/5 dark:bg-slate-900 border border-slate-200/80 dark:border-slate-800 min-h-[260px]">
                          {file.previewUrl && !imgFailed ? (
                            <img
                              src={file.previewUrl}
                              alt={file.name}
                              referrerPolicy="no-referrer"
                              onError={() => setImgFailed(true)}
                              className={`max-h-[360px] w-auto object-contain transition-transform duration-200 ${
                                imgZoomed ? 'scale-125 cursor-zoom-out' : 'cursor-zoom-in'
                              }`}
                              onClick={() => setImgZoomed((z) => !z)}
                            />
                          ) : (
                            <div className="flex flex-col items-center justify-center p-8 text-center">
                              <ImageIcon className="w-12 h-12 text-sky-500 mb-3" />
                              <p dir="ltr" className="text-sm font-medium text-slate-800 dark:text-slate-200">
                                {file.name}
                              </p>
                              <p className="text-xs text-slate-500 dark:text-slate-400 font-mono tabular-nums mt-1">
                                {t.preview.rawLosslessImage} ·{' '}
                                <span dir="ltr">{formatBytes(file.size, 2, locale)}</span>
                              </p>
                            </div>
                          )}
                        </div>
                      )}

                      {/* AUDIO PREVIEW —— 直接播放服务端流，不做假进度 */}
                      {file.category === 'audio' && (
                        <div className="flex-1 flex flex-col justify-between gap-4 rounded-lg bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 p-5">
                          <div className="flex items-center gap-4">
                            <div className="w-14 h-14 rounded-xl bg-sky-500/10 border border-sky-500/20 flex items-center justify-center text-sky-600 dark:text-sky-400 shrink-0">
                              <Music className="w-7 h-7" />
                            </div>
                            <div className="min-w-0">
                              <p dir="ltr" className="text-sm font-semibold text-slate-900 dark:text-slate-100 truncate">
                                {file.name}
                              </p>
                              <p className="text-xs text-slate-500 dark:text-slate-400 font-mono tabular-nums mt-0.5">
                                <span dir="ltr">{formatBytes(file.size, 2, locale)}</span>
                              </p>
                            </div>
                          </div>
                          <audio
                            controls
                            preload="metadata"
                            src={streamUrl}
                            aria-label={t.files.detailPlay}
                            className="w-full"
                          >
                            {t.preview.noInlinePlayback}
                          </audio>
                        </div>
                      )}

                      {/* VIDEO PREVIEW —— 直接播放服务端流 */}
                      {file.category === 'video' && (
                        <div className="flex-1 flex flex-col rounded-lg bg-slate-900 border border-slate-800 overflow-hidden">
                          <video
                            controls
                            preload="metadata"
                            playsInline
                            src={streamUrl}
                            aria-label={t.files.detailPlay}
                            className="w-full max-h-[360px] bg-black"
                          >
                            {t.preview.noInlinePlayback}
                          </video>
                        </div>
                      )}

                      {/* PDF PREVIEW —— 浏览器内嵌查看器 */}
                      {file.category === 'pdf' && (
                        <div className="flex-1 flex flex-col rounded-lg bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 overflow-hidden min-h-[360px]">
                          <iframe
                            src={streamUrl}
                            title={file.name}
                            className="w-full flex-1 min-h-[360px]"
                          />
                        </div>
                      )}

                      {/* ARCHIVE / OTHER DOCUMENT PREVIEW */}
                      {(file.category === 'archive' ||
                        file.category === 'document' ||
                        file.category === 'folder') && (
                        <div className="flex-1 flex flex-col items-center justify-center rounded-lg bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 p-8 text-center">
                          {file.isDir ? (
                            <Folder className="w-12 h-12 text-sky-500 mb-3" />
                          ) : (
                            <HardDrive className="w-12 h-12 text-slate-500 mb-3" />
                          )}
                          <p dir="ltr" className="text-sm font-semibold text-slate-900 dark:text-slate-100">
                            {file.name}
                          </p>
                          <p className="text-xs text-slate-500 dark:text-slate-400 font-mono tabular-nums mt-1">
                            <span dir="ltr">{formatBytes(file.size, 2, locale)}</span>
                          </p>
                        </div>
                      )}
                    </div>

                    {/* Right/Bottom: Structured Metadata & Primary Actions */}
                    <div className="lg:col-span-4 p-6 flex flex-col justify-between space-y-6">
                      <div className="space-y-4">
                        {file.thumbnailUrl && !imgFailed && (
                          <div>
                            <span className="block text-xs text-slate-500 dark:text-slate-400 mb-1.5">
                              {t.files.detailThumbnail}
                            </span>
                            <img
                              src={file.thumbnailUrl}
                              alt={`${file.name} thumbnail`}
                              referrerPolicy="no-referrer"
                              onError={() => setImgFailed(true)}
                              className="w-24 h-16 object-cover rounded-lg border border-slate-200 dark:border-slate-800"
                            />
                          </div>
                        )}

                        <dl className="space-y-3 text-xs">
                          <div className="flex justify-between gap-2 py-2 border-b border-slate-100 dark:border-slate-800/80">
                            <dt className="text-slate-500 dark:text-slate-400">
                              {t.files.detailCategory}
                            </dt>
                            <dd className="font-mono text-slate-900 dark:text-slate-100">
                              {t.categories[file.category] || file.category}
                            </dd>
                          </div>
                          <div className="flex justify-between gap-2 py-2 border-b border-slate-100 dark:border-slate-800/80">
                            <dt className="text-slate-500 dark:text-slate-400">
                              {t.files.detailSize}
                            </dt>
                            <dd
                              dir="ltr"
                              className="font-mono tabular-nums text-slate-900 dark:text-slate-100 text-end"
                            >
                              {formatBytes(file.size, 2, locale)} ({formatNumber(file.size, locale)} B)
                            </dd>
                          </div>
                          <div className="flex justify-between gap-2 py-2 border-b border-slate-100 dark:border-slate-800/80">
                            <dt className="text-slate-500 dark:text-slate-400">
                              {t.files.detailUpdated}
                            </dt>
                            <dd className="font-mono tabular-nums text-slate-900 dark:text-slate-100">
                              {formatDateTime(file.updatedAt, locale)}
                            </dd>
                          </div>
                          <div className="py-2">
                            <dt className="text-slate-500 dark:text-slate-400 mb-1">
                              {t.files.detailPath}
                            </dt>
                            <dd
                              dir="ltr"
                              className="font-mono text-[11px] text-slate-800 dark:text-slate-200 break-all bg-slate-100 dark:bg-slate-800/70 px-2.5 py-1.5 rounded-md text-left"
                            >
                              {fullPath}
                            </dd>
                          </div>
                        </dl>
                      </div>

                      {!file.isDir && (
                        <div className="space-y-2.5 pt-4 border-t border-slate-200 dark:border-slate-800">
                          <Button
                            type="button"
                            variant="accent"
                            size="lg"
                            onClick={() => onDownload(file)}
                            className="w-full"
                          >
                            <Download className="w-4 h-4" />
                            <span>{t.files.actionDownload}</span>
                          </Button>
                          <Button
                            type="button"
                            variant="outline"
                            size="lg"
                            onClick={() => onSaveOffline(file)}
                            disabled={isOfflineSaved}
                            className="w-full"
                          >
                            <HardDrive className="w-4 h-4" />
                            <span>
                              {isOfflineSaved
                                ? t.files.actionOfflineSaved
                                : t.files.actionSaveOffline}
                            </span>
                          </Button>
                        </div>
                      )}
                    </div>
                  </div>
                </>
              );
            })()}
          </motion.div>
        </motion.div>
      )}
    </AnimatePresence>
  );
};
