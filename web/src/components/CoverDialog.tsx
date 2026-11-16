import React, { useEffect, useRef, useState } from 'react';
import { Image as ImageIcon, X } from 'lucide-react';
import { Translations } from '../utils/i18n';
import { Button } from './ui';
import { Modal } from './Modal';

interface CoverDialogProps {
  open: boolean;
  volumeName: string;
  /** 当前频道标题；弹窗打开时作为输入框初值 */
  currentTitle?: string;
  t: Translations;
  busy: boolean;
  onClose: () => void;
  /** 提交原图；服务端会裁成 512×512 后写成频道头像 */
  onSubmit: (file: File) => void;
  /** 提交频道标题；没有提供时隐藏该输入框 */
  onSubmitTitle?: (title: string) => void;
}

// CoverDialog 是「设置频道头像与名字」的弹窗：改名字、选图、预览、保存。
export const CoverDialog: React.FC<CoverDialogProps> = ({
  open,
  volumeName,
  currentTitle,
  t,
  busy,
  onClose,
  onSubmit,
  onSubmitTitle,
}) => {
  const inputRef = useRef<HTMLInputElement | null>(null);
  const [picked, setPicked] = useState<File | null>(null);
  const [preview, setPreview] = useState('');
  const [title, setTitle] = useState(currentTitle ?? '');

  // 每次打开都用最新的频道标题初始化输入框
  useEffect(() => {
    if (open) setTitle(currentTitle ?? '');
  }, [open, currentTitle]);

  const titleChanged = (currentTitle ?? '').trim() !== title.trim();

  // 每次关闭都清掉已选图片与预览 URL，避免下次打开显示上一张
  useEffect(() => {
    if (!open) {
      setPicked(null);
      setPreview('');
    }
  }, [open]);

  useEffect(() => {
    if (!picked) return;
    const url = URL.createObjectURL(picked);
    setPreview(url);
    return () => URL.revokeObjectURL(url);
  }, [picked]);

  if (!open) return null;

  return (
    <Modal
      onClose={onClose}
      labelledBy="cover-dialog-title"
      closeDisabled={busy}
      panelClassName="w-full max-w-md rounded-xl surface-card p-6 space-y-4 shadow-xl"
    >
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <h3 id="cover-dialog-title" className="text-base font-semibold text-zinc-950 dark:text-white">
              {t.volume.coverDialogTitle}
            </h3>
            <p className="mt-1 text-xs text-zinc-500 dark:text-zinc-400 font-mono truncate" dir="ltr">
              {volumeName}
            </p>
          </div>
          <button
            type="button"
            onClick={onClose}
            disabled={busy}
            aria-label={t.volume.coverDialogCancel}
            className="p-1.5 rounded-lg text-zinc-500 hover:text-zinc-900 dark:hover:text-white hover:bg-zinc-100 dark:hover:bg-zinc-800 disabled:opacity-50"
          >
            <X className="w-4 h-4" />
          </button>
        </div>

        {onSubmitTitle && (
          <div className="space-y-1.5">
            <label htmlFor="cover-dialog-name" className="block text-xs font-medium text-zinc-600 dark:text-zinc-300">
              {t.volume.coverDialogNameLabel}
            </label>
            <input
              id="cover-dialog-name"
              type="text"
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              disabled={busy}
              maxLength={128}
              placeholder={t.volume.coverDialogNamePlaceholder}
              className="w-full rounded-lg border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 px-3 py-2 text-sm text-zinc-900 dark:text-white focus:outline-none focus:ring-2 focus:ring-[#007AFF]/40"
            />
          </div>
        )}

        <div className="space-y-1.5">
          <button
            type="button"
            onClick={() => inputRef.current?.click()}
            className="relative block w-full aspect-square max-h-64 rounded-lg overflow-hidden border border-dashed border-zinc-300 dark:border-zinc-700 bg-zinc-50 dark:bg-zinc-800/60"
          >
            {preview ? (
              <img src={preview} alt="" className="w-full h-full object-cover" />
            ) : (
              <span className="absolute inset-0 flex flex-col items-center justify-center gap-2 text-zinc-400 dark:text-zinc-500">
                <ImageIcon className="w-8 h-8" />
                <span className="text-xs">{t.volume.coverDialogPick}</span>
              </span>
            )}
          </button>
          <input
            ref={inputRef}
            type="file"
            accept="image/*"
            className="hidden"
            onChange={(e) => {
              const f = e.target.files?.[0] ?? null;
              if (f) setPicked(f);
              e.target.value = '';
            }}
          />
        </div>

        <div className="flex items-center justify-end gap-2 pt-2">
          <Button type="button" variant="outline" onClick={onClose} disabled={busy}>
            {t.volume.coverDialogCancel}
          </Button>
          <Button
            type="button"
            variant="accent"
            onClick={() => {
              if (titleChanged && title.trim()) onSubmitTitle?.(title.trim());
              if (picked) onSubmit(picked);
            }}
            disabled={(!picked && !titleChanged) || busy || (titleChanged && !title.trim())}
          >
            {busy ? t.volume.coverDialogSaving : t.volume.coverDialogSave}
          </Button>
        </div>
    </Modal>
  );
};
