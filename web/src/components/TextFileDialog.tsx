import React, { useId, useState } from 'react';
import { Translations } from '../utils/i18n';
import { Modal } from './Modal';
import { Button, Input } from './ui';

/**
 * validateTextDraft 校验「新建文本」表单。
 *
 * 输入：name 原始文件名；text 正文
 * 输出：错误码或 null（通过）
 * 预期：后端 upload_api 拒绝 size<=0，所以空正文在提交前就挡住；
 *       / 与 \ 会被当成目录分隔符，直接拒绝而不是悄悄改写
 */
export function validateTextDraft(
  name: string,
  text: string
): 'name_invalid' | 'body_empty' | null {
  const trimmed = name.trim();
  if (!trimmed || trimmed.includes('/') || trimmed.includes('\\')) return 'name_invalid';
  if (text.length === 0) return 'body_empty';
  return null;
}

/**
 * TextFileDialogProps 是新建文本弹窗的入参。
 *
 * 输入：initialText 预填正文（剪贴板粘贴的纯文本）；onSave 提交文件名与正文
 * 输出：无
 * 预期：弹窗只负责收集内容，创建 File 与上传由调用方接手
 */
export interface TextFileDialogProps {
  t: Translations;
  initialText?: string;
  onSave: (name: string, text: string) => void;
  onClose: () => void;
}

/**
 * TextFileDialog 在面板里新建一个文本文件。
 *
 * 输入：见 TextFileDialogProps
 * 输出：native <dialog> 表单
 * 预期：保存后得到一个纯文本 File，走与其它文件相同的上传计划，
 *       不给后端增加任何专用接口
 */
export const TextFileDialog: React.FC<TextFileDialogProps> = ({
  t,
  initialText = '',
  onSave,
  onClose,
}) => {
  const titleId = useId();
  const nameId = useId();
  const bodyId = useId();
  const [name, setName] = useState('untitled.txt');
  const [text, setText] = useState(initialText);
  const [error, setError] = useState('');

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    const verdict = validateTextDraft(name, text);
    if (verdict === 'name_invalid') {
      setError(t.files.textDialogNameInvalid);
      return;
    }
    if (verdict === 'body_empty') {
      setError(t.files.textDialogBodyEmpty);
      return;
    }
    onSave(name.trim(), text);
  };

  return (
    <Modal onClose={onClose} labelledBy={titleId} panelClassName="w-full max-w-lg">
      <form
        onSubmit={submit}
        className="w-full max-w-lg rounded-xl surface-card p-6 space-y-4 shadow-xl"
      >
        <h3
          id={titleId}
          className="text-base font-semibold tracking-tight text-zinc-900 dark:text-white"
        >
          {t.files.textDialogTitle}
        </h3>

        <div className="space-y-1.5">
          <label
            htmlFor={nameId}
            className="block text-xs font-medium text-zinc-700 dark:text-zinc-300"
          >
            {t.files.textDialogNameLabel}
          </label>
          <Input
            id={nameId}
            type="text"
            autoFocus={!initialText}
            value={name}
            onChange={(e) => {
              setName(e.target.value);
              setError('');
            }}
            placeholder={t.files.textDialogNamePlaceholder}
          />
        </div>

        <div className="space-y-1.5">
          <label
            htmlFor={bodyId}
            className="block text-xs font-medium text-zinc-700 dark:text-zinc-300"
          >
            {t.files.textDialogBodyLabel}
          </label>
          <textarea
            id={bodyId}
            aria-invalid={error ? true : undefined}
            autoFocus={Boolean(initialText)}
            value={text}
            onChange={(e) => {
              setText(e.target.value);
              setError('');
            }}
            placeholder={t.files.textDialogBodyPlaceholder}
            rows={10}
            className="flex w-full resize-y rounded-xl border border-zinc-200/90 bg-white px-3 py-2 font-mono text-[13px] leading-relaxed text-zinc-900 shadow-2xs transition-colors placeholder:text-zinc-400 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#007AFF]/35 focus-visible:border-[#007AFF] dark:border-zinc-800 dark:bg-zinc-900/70 dark:text-zinc-100 dark:placeholder:text-zinc-500"
          />
        </div>

        {error ? (
          <p role="alert" className="text-xs text-[#FF3B30] dark:text-[#FF453A]">
            {error}
          </p>
        ) : null}

        <div className="flex items-center justify-end gap-2 pt-2">
          <Button type="button" variant="outline" onClick={onClose}>
            {t.files.cancel}
          </Button>
          <Button type="submit" variant="accent">
            {t.files.textDialogSave}
          </Button>
        </div>
      </form>
    </Modal>
  );
};
