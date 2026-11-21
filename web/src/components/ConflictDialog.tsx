import React, { useId, useState } from 'react';
import { Translations, interpolate } from '../utils/i18n';
import { relDirPrefix, type StagedUpload } from '../utils/uploadPlan';
import type { ConflictAction, UploadConflict } from '../utils/uploadConflict';
import { Modal } from './Modal';
import { Button, Select } from './ui';

/**
 * ConflictDialogProps 是「同名文件怎么处理」弹窗的入参。
 *
 * 输入：items 本批待传草稿；conflicts 检测出的冲突项；onConfirm 交回每项的处理方式
 * 输出：无
 * 预期：只做选择，不发任何请求；关掉弹窗等于回到上传清单继续改
 */
export interface ConflictDialogProps {
  t: Translations;
  items: StagedUpload[];
  conflicts: UploadConflict[];
  onConfirm: (choices: Record<string, ConflictAction>) => void;
  onCancel: () => void;
}

/** 每行的可选动作，顺序即下拉选项顺序。 */
const ACTIONS: readonly ConflictAction[] = ['rename', 'overwrite', 'skip'];

/**
 * ConflictDialog 让用户逐个决定同名文件是覆盖、改名还是跳过。
 *
 * 输入：见 ConflictDialogProps
 * 输出：native <dialog> 形式的冲突弹窗
 * 预期：
 *   - 默认全部「改名」，不覆盖任何已有文件
 *   - 每行一个下拉，读屏标签带文件名；顶部的「全部…」一键统一
 *   - 全部选跳过时禁用确认，避免发出一份空上传计划
 *   - 用原生 <dialog>，Esc 与焦点归还交给浏览器
 */
export const ConflictDialog: React.FC<ConflictDialogProps> = ({
  t,
  items,
  conflicts,
  onConfirm,
  onCancel,
}) => {
  const titleId = useId();
  // 缺省改名：与 resolveUploadConflicts 的默认值一致，两边不各写一套
  const [choices, setChoices] = useState<Record<string, ConflictAction>>(() =>
    Object.fromEntries(conflicts.map((c) => [c.id, 'rename' as ConflictAction]))
  );

  const prefixOf = (id: string) => {
    const item = items.find((it) => it.id === id);
    return item ? relDirPrefix(item.rel) : '';
  };
  const actionOf = (id: string) => choices[id] ?? 'rename';
  const allSkip = conflicts.every((c) => actionOf(c.id) === 'skip');

  const pick = (id: string, action: ConflictAction) => {
    setChoices((prev) => ({ ...prev, [id]: action }));
  };
  const pickAll = (action: ConflictAction) => {
    setChoices(Object.fromEntries(conflicts.map((c) => [c.id, action])));
  };

  return (
    <Modal onClose={onCancel} labelledBy={titleId} panelClassName="w-full max-w-lg sm:max-w-xl">
      <div className="w-full rounded-2xl surface-card shadow-2xl flex flex-col max-h-[calc(100dvh-2.5rem)] overflow-hidden">
        <div className="p-5 sm:p-6 pb-3 shrink-0 border-b border-zinc-200/60 dark:border-zinc-800/60 space-y-3">
          <div className="space-y-1">
            <h3
              id={titleId}
              className="text-base font-semibold tracking-tight text-zinc-900 dark:text-white"
            >
              {t.files.conflictTitle}
            </h3>
            <p className="text-xs leading-relaxed text-zinc-500 dark:text-zinc-400">
              {t.files.conflictHint}
            </p>
          </div>

          <div className="flex flex-wrap gap-2">
            <Button type="button" variant="outline" size="sm" onClick={() => pickAll('overwrite')}>
              {t.files.conflictAllOverwrite}
            </Button>
            <Button type="button" variant="outline" size="sm" onClick={() => pickAll('rename')}>
              {t.files.conflictAllRename}
            </Button>
            <Button type="button" variant="outline" size="sm" onClick={() => pickAll('skip')}>
              {t.files.conflictAllSkip}
            </Button>
          </div>
        </div>

        <ul className="flex-1 min-h-0 overflow-y-auto custom-scrollbar px-5 sm:px-6 divide-y divide-zinc-200/70 dark:divide-zinc-800/70 overscroll-contain">
          {conflicts.map((conflict) => {
            const action = actionOf(conflict.id);
            const selectId = `${titleId}-${conflict.id}`;
            const path = `${prefixOf(conflict.id)}${conflict.name}`;
            return (
              <li key={conflict.id} className="flex flex-col gap-2 py-3 sm:flex-row sm:items-center">
                <div className="min-w-0 flex-1">
                  <p
                    dir="auto"
                    className="truncate text-sm font-medium text-zinc-900 dark:text-zinc-100"
                  >
                    {path}
                  </p>
                  {action === 'rename' && (
                    <p className="mt-0.5 truncate text-[11px] text-zinc-500 dark:text-zinc-400" dir="auto">
                      {interpolate(t.files.conflictSuggested, { name: conflict.suggested })}
                    </p>
                  )}
                </div>
                {/* 每行一个真实 label：读屏能念出改的是哪个文件 */}
                <label htmlFor={selectId} className="sr-only">
                  {`${t.files.conflictRowLabel}: ${path}`}
                </label>
                <Select
                  id={selectId}
                  value={action}
                  onChange={(event) => pick(conflict.id, event.target.value as ConflictAction)}
                  className="h-8 shrink-0 text-xs"
                >
                  {ACTIONS.map((value) => (
                    <option key={value} value={value} className="bg-white dark:bg-zinc-900">
                      {value === 'rename'
                        ? t.files.conflictRename
                        : value === 'overwrite'
                        ? t.files.conflictOverwrite
                        : t.files.conflictSkip}
                    </option>
                  ))}
                </Select>
              </li>
            );
          })}
        </ul>

        <div className="p-4 sm:p-5 pt-3 shrink-0 border-t border-zinc-200/60 dark:border-zinc-800/60 flex items-center justify-end gap-2 bg-zinc-50/50 dark:bg-zinc-900/50">
          <Button type="button" variant="outline" onClick={onCancel}>
            {t.files.cancel}
          </Button>
          <Button
            type="button"
            variant="accent"
            autoFocus
            disabled={conflicts.length === 0 || allSkip}
            onClick={() => onConfirm(choices)}
          >
            {t.files.conflictConfirm}
          </Button>
        </div>
      </div>
    </Modal>
  );
};
