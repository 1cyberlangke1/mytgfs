import React, { useEffect, useId, useState } from 'react';
import { File as FileIcon, X } from 'lucide-react';
import type { Locale } from '../types';
import { formatBytes, inferFileCategory } from '../utils/fsLogic';
import { Translations, interpolate } from '../utils/i18n';
import {
  keptStaged,
  stagedDisplayPath,
  stagedTotalBytes,
  type StagedUpload,
} from '../utils/uploadPlan';
import { Modal } from './Modal';
import { Button } from './ui';

/**
 * UploadPlanDialogProps 是「上传前清单」的入参。
 *
 * 输入：items 全部草稿；onConfirm 确认要传的剩余项；onCancel 放弃整批
 * 输出：无
 * 预期：清单是最后一道反悔闸门——点「开始上传」之前不发任何请求
 */
export interface UploadPlanDialogProps {
  t: Translations;
  locale: Locale;
  items: StagedUpload[];
  onConfirm: (kept: StagedUpload[]) => void;
  onCancel: () => void;
}

/**
 * PlanThumb 渲染清单左侧的预览格。
 *
 * 输入：file 待上传文件
 * 输出：图片显示缩略图，其它类型显示通用文件图标
 * 预期：剪贴板粘贴进来的图片名字是 pasted-<时间戳>.png，光看名字认不出，
 *       缩略图让用户确认要传的到底是哪张图；对象 URL 随卸载释放
 */
function PlanThumb({ file }: { file: File }) {
  const isImage = inferFileCategory(file.name, false) === 'image';
  const [url, setUrl] = useState('');

  useEffect(() => {
    if (!isImage) {
      setUrl('');
      return;
    }
    const next = URL.createObjectURL(file);
    setUrl(next);
    return () => URL.revokeObjectURL(next);
  }, [file, isImage]);

  return (
    <span className="grid h-10 w-10 shrink-0 place-items-center overflow-hidden rounded-lg bg-zinc-100 dark:bg-zinc-800">
      {url ? (
        <img src={url} alt="" className="h-full w-full object-cover" />
      ) : (
        <FileIcon className="h-4 w-4 text-zinc-400 dark:text-zinc-500" aria-hidden="true" />
      )}
    </span>
  );
}

/**
 * UploadPlanDialog 让用户在开始上传前剔除不要的文件。
 *
 * 输入：见 UploadPlanDialogProps
 * 输出：native <dialog> 形式的清单弹窗
 * 预期：
 *   - 每行一个 ✕，移除后该文件不再出现在清单里，也不会发出任何请求
 *   - 「全部移除」把整批移出；「恢复全选」把它们放回来
 *   - 清空后「开始上传」禁用，避免发出一份空计划
 *   - 清单会随外部追加（拖拽/剪贴板）继续增长，不用关掉重开
 */
export const UploadPlanDialog: React.FC<UploadPlanDialogProps> = ({
  t,
  locale,
  items,
  onConfirm,
  onCancel,
}) => {
  const titleId = useId();
  // 被移除的 id：保留而不是直接删，才能支持「恢复全选」
  const [hidden, setHidden] = useState<Set<string>>(() => new Set());

  const rows = keptStaged(items, hidden);
  const totalBytes = stagedTotalBytes(rows);
  const summary = interpolate(
    t.files.uploadPlanSummary,
    { count: rows.length, size: formatBytes(totalBytes, 1, locale) },
    locale
  );

  const hideOne = (id: string) => {
    setHidden((prev) => {
      const next = new Set(prev);
      next.add(id);
      return next;
    });
  };

  const hideAll = () => setHidden(new Set(items.map((item) => item.id)));
  const restoreAll = () => setHidden(new Set());

  return (
    <Modal onClose={onCancel} labelledBy={titleId} panelClassName="w-full max-w-lg">
      <div className="w-full max-w-lg rounded-xl surface-card p-6 space-y-4 shadow-xl">
        <div className="space-y-1">
          <h3
            id={titleId}
            className="text-base font-semibold tracking-tight text-zinc-900 dark:text-white"
          >
            {t.files.uploadPlanTitle}
          </h3>
          <p className="text-xs tabular-nums text-zinc-500 dark:text-zinc-400">{summary}</p>
        </div>

        {rows.length > 0 ? (
          <ul className="max-h-[min(50vh,20rem)] overflow-y-auto divide-y divide-zinc-200/70 dark:divide-zinc-800/70">
            {rows.map((item) => {
              const path = stagedDisplayPath(item);
              return (
                <li key={item.id} className="flex items-center gap-3 py-2">
                  <PlanThumb file={item.file} />
                  <div className="min-w-0 flex-1">
                    <p
                      className="truncate text-sm text-zinc-900 dark:text-zinc-100"
                      title={path}
                      dir="auto"
                    >
                      {item.file.name}
                    </p>
                    {item.rel ? (
                      <p
                        className="truncate text-[11px] font-mono text-zinc-500 dark:text-zinc-400"
                        dir="ltr"
                        title={item.rel}
                      >
                        {item.rel}
                      </p>
                    ) : null}
                  </div>
                  <span className="shrink-0 text-[11px] font-mono tabular-nums text-zinc-500 dark:text-zinc-400">
                    {formatBytes(item.file.size, 1, locale)}
                  </span>
                  <button
                    type="button"
                    onClick={() => hideOne(item.id)}
                    aria-label={`${t.files.uploadPlanRemove}: ${path}`}
                    title={t.files.uploadPlanRemove}
                    className="shrink-0 rounded-lg p-1.5 text-zinc-500 hover:bg-zinc-100 hover:text-zinc-900 dark:text-zinc-400 dark:hover:bg-zinc-800 dark:hover:text-white"
                  >
                    <X className="h-3.5 w-3.5" />
                  </button>
                </li>
              );
            })}
          </ul>
        ) : (
          <p className="py-6 text-center text-sm text-zinc-500 dark:text-zinc-400">
            {t.files.uploadPlanEmpty}
          </p>
        )}

        <div className="flex items-center justify-between gap-2 pt-1">
          <Button
            type="button"
            variant="ghost"
            onClick={hidden.size > 0 ? restoreAll : hideAll}
            disabled={items.length === 0}
          >
            {hidden.size > 0 ? t.files.uploadPlanRestoreAll : t.files.uploadPlanRemoveAll}
          </Button>
          <div className="flex items-center gap-2">
            <Button type="button" variant="outline" onClick={onCancel}>
              {t.files.cancel}
            </Button>
            <Button
              type="button"
              variant="accent"
              autoFocus
              disabled={rows.length === 0}
              onClick={() => onConfirm(rows)}
            >
              {t.files.uploadPlanConfirm}
            </Button>
          </div>
        </div>
      </div>
    </Modal>
  );
};
