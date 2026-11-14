import React, { useEffect, useRef, useState } from 'react';
import { Image as ImageIcon, X } from 'lucide-react';
import { Translations } from '../utils/i18n';
import { Button } from './ui';

interface CoverDialogProps {
  open: boolean;
  volumeName: string;
  t: Translations;
  busy: boolean;
  onClose: () => void;
  /** 提交原图；服务端会裁成 512×512 后写成频道头像 */
  onSubmit: (file: File) => void;
}

// CoverDialog 是「设置频道头像」的弹窗：选图、预览、保存。
export const CoverDialog: React.FC<CoverDialogProps> = ({
  open,
  volumeName,
  t,
  busy,
  onClose,
  onSubmit,
}) => {
  const inputRef = useRef<HTMLInputElement | null>(null);
  const [picked, setPicked] = useState<File | null>(null);
  const [preview, setPreview] = useState('');

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

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !busy) onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [open, busy, onClose]);

  if (!open) return null;

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-xs p-4"
      role="dialog"
      aria-modal="true"
      aria-labelledby="cover-dialog-title"
    >
      <div className="w-full max-w-md rounded-xl surface-card p-6 space-y-4 shadow-xl">
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
            onClick={() => picked && onSubmit(picked)}
            disabled={!picked || busy}
          >
            {busy ? t.volume.coverDialogSaving : t.volume.coverDialogSave}
          </Button>
        </div>
      </div>
    </div>
  );
};
