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
import { motion } from 'motion/react';
import { FileItem, Locale } from '../types';
import { fileURL, thumbURL } from '../api';
import { formatBytes, getItemFullPath } from '../utils/fsLogic';
import { Translations, formatDateTime, formatNumber } from '../utils/i18n';
import { Button, IOS_SPRING } from './ui';
import { Modal } from './Modal';

/**
 * INLINE_PREVIEW_MAX_BYTES 是内联预览的体积上限。
 *
 * 桌面壳（Wails responsewriter_windows.go 用 bytes.Buffer，且明确不实现
 * http.Flusher）与安卓壳（nativeServeAsset 返回整份 byte[]）的出口都只能
 * 「整份交一次」，所以图片/文本/PDF 一旦内联就必然整份进内存。超过这个值改成
 * 「下载后用系统应用打开」，两个平台走同一条路，不再按平台分支。
 *
 * 视频与音频不吃这个限制：它们走 /api/file 的 Range 窗口，服务端每次只回
 * 一段（安卓壳 1 MiB、Go 侧窗口同样有上限），内存里始终只有当前窗口。
 */
const INLINE_PREVIEW_MAX_BYTES = 32 << 20;

/**
 * inlinePreviewBlocked 判断某条目是否只能下载、不能内嵌预览。
 *
 * 输入：size 字节数；category 文件分类；isDir 是否目录；hasStream 是否有本地流覆盖
 * 输出：true 表示应显示「文件较大，请下载后打开」
 * 预期：视频/音频永远走 Range 窗口，不受体积上限约束；目录与本地流覆盖也不拦
 */
export function inlinePreviewBlocked(
  size: number,
  category: string,
  isDir: boolean,
  hasStream: boolean
): boolean {
  if (isDir || hasStream) return false;
  if (category === 'video' || category === 'audio') return false;
  return size > INLINE_PREVIEW_MAX_BYTES;
}

interface FilePreviewModalProps {
  file: FileItem | null;
  allFiles: FileItem[];
  t: Translations;
  locale: Locale;
  onClose: () => void;
  onDownload: (file: FileItem) => void;
  /** 覆盖媒体源：下载页预览本地文件时指向 /api/downloads/file */
  streamOverride?: string;
  /** 主按钮文案；本地文件是「打开」而不是「下载」 */
  actionLabel?: string;
  /** 设备能力：安卓没有下载目录，主操作按钮不渲染 */
  canDownload?: boolean;
}

export const FilePreviewModal: React.FC<FilePreviewModalProps> = ({
  file,
  allFiles,
  t,
  locale,
  onClose,
  onDownload,
  streamOverride,
  actionLabel,
  canDownload = true,
}) => {
  const [imgZoomed, setImgZoomed] = useState(false);
  const [imgFailed, setImgFailed] = useState(false);
  // 图片先显示缩略图：原图动辄几 MB，而桌面壳与安卓 WebView 都会把整份响应
  // 读进内存之后才交给页面，直接挂 previewUrl 等于每次预览都整份下载
  const [showOriginal, setShowOriginal] = useState(false);

  useEffect(() => {
    setImgZoomed(false);
    setImgFailed(false);
    setShowOriginal(false);
  }, [file?.id]);

  if (!file) return null;

  return (
    <Modal
      onClose={onClose}
      labelledBy="file-detail-modal-title"
      panelClassName="w-full max-w-4xl"
    >
      <motion.div
        initial={{ opacity: 0, scale: 0.94, y: 12 }}
        animate={{ opacity: 1, scale: 1, y: 0 }}
        transition={IOS_SPRING}
        className="w-full max-w-4xl rounded-2xl bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 shadow-xl overflow-hidden my-auto max-h-[calc(100dvh-2.5rem)] flex flex-col"
      >
            {(() => {
              const fullPath = getItemFullPath(allFiles, file);
              // 真实媒体流地址：服务端 /api/file 支持 Range，可直接喂给 video/audio/iframe
              const streamUrl = streamOverride ?? fileURL(file.volumeId, fullPath, true);
              // 缩略图只在读远端文件时有意义：本地下载预览直接用 streamOverride
              const thumbUrl = streamOverride ? '' : thumbURL(file.volumeId, fullPath);
              const imgSrc = showOriginal || !thumbUrl ? file.previewUrl : thumbUrl;
              // 视频/音频走 /api/file 的 Range 窗口，内存只占当前窗口，
              // 因此不受 32 MiB 内联上限约束；图片/文本/PDF 仍然整份读取。
              const inlineTooBig = inlinePreviewBlocked(
                file.size,
                file.category,
                file.isDir,
                Boolean(streamOverride)
              );
              const tooBigCard = (
                <div className="flex-1 flex flex-col items-center justify-center gap-2 rounded-lg bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 p-8 text-center">
                  <HardDrive className="w-10 h-10 text-slate-500 mb-1" />
                  <p dir="ltr" className="text-sm font-semibold text-slate-900 dark:text-slate-100">
                    {file.name}
                  </p>
                  <p className="text-xs text-slate-500 dark:text-slate-400">
                    {t.preview.tooLargeToPreview}
                  </p>
                  <p className="text-xs text-slate-500 dark:text-slate-400 font-mono tabular-nums">
                    <span dir="ltr">{formatBytes(file.size, 2, locale)}</span>
                  </p>
                </div>
              );

              return (
                <>
                  {/* Header */}
                  <div className="flex items-center justify-between gap-4 px-6 py-4 border-b border-slate-200 dark:border-slate-800 shrink-0">
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

                  <div className="grid grid-cols-1 lg:grid-cols-12 flex-1 min-h-0 overflow-y-auto custom-scrollbar">
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
                            onClick={() => {
                              setImgFailed(false);
                              setShowOriginal((v) => !v);
                            }}
                            aria-pressed={showOriginal}
                          >
                            {showOriginal ? t.preview.showThumbnail : t.preview.showOriginal}
                          </Button>
                        )}
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
                              src={imgSrc}
                              alt={file.name}
                              referrerPolicy="no-referrer"
                              onError={() => {
                                // 缩略图缺失/失败时退回原图一次，而不是整块变占位
                                if (!showOriginal && thumbUrl) {
                                  setShowOriginal(true);
                                  return;
                                }
                                setImgFailed(true);
                              }}
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
                              {imgFailed && (
                                <Button
                                  type="button"
                                  variant="ghost"
                                  className="mt-3"
                                  onClick={() => {
                                    // 失败可能只是瞬时网络问题：先退回缩略图，
                                    // 仍失败再由 onError 决定是否继续降级
                                    setImgFailed(false);
                                    setShowOriginal(false);
                                  }}
                                >
                                  {t.errors.retry}
                                </Button>
                              )}
                            </div>
                          )}
                        </div>
                      )}

                      {/* AUDIO PREVIEW —— 直接播放服务端流，不做假进度 */}
                      {file.category === 'audio' && (inlineTooBig ? tooBigCard : (
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
                      ))}

                      {/* VIDEO PREVIEW —— 直接播放服务端流 */}
                      {file.category === 'video' && (inlineTooBig ? tooBigCard : (
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
                      ))}

                      {/* PDF PREVIEW —— 浏览器内嵌查看器 */}
                      {file.category === 'pdf' && (inlineTooBig ? tooBigCard : (
                        <div className="flex-1 flex flex-col rounded-lg bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 overflow-hidden min-h-[360px]">
                          <iframe
                            src={streamUrl}
                            title={file.name}
                            className="w-full flex-1 min-h-[360px]"
                          />
                        </div>
                      ))}

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

                      {!file.isDir && canDownload && (
                        <div className="space-y-2.5 pt-4 border-t border-slate-200 dark:border-slate-800">
                          <Button
                            type="button"
                            variant="accent"
                            size="lg"
                            onClick={() => onDownload(file)}
                            className="w-full"
                          >
                            <Download className="w-4 h-4" />
                            <span>{actionLabel ?? t.files.actionDownload}</span>
                          </Button>
                        </div>
                      )}
                    </div>
                  </div>
                </>
              );
            })()}
          </motion.div>
    </Modal>
  );
};
