import React from 'react';
import { AlertTriangle, List, Loader2 } from 'lucide-react';
import type { FileItem, Locale, VolumeInfo } from '../types';
import { Translations, formatPlural, interpolate } from '../utils/i18n';
import { canMoveItemsToFolder } from '../utils/fsLogic';
import { FolderTree } from './FolderTree';
import { Modal } from './Modal';
import { Button, Input } from './ui';
import { joinPath, targetPathText, validateEntryName } from './filesHelpers';

export interface ChannelOption {
  id: number;
  chat_id: number;
  title: string;
  username: string;
  creator: boolean;
  admin: boolean;
}

interface FilesDialogsProps {
  t: Translations;
  locale: Locale;
  files: FileItem[];
  volumeDirectories: FileItem[];
  currentVolume: VolumeInfo | undefined;
  activeVolumeId: string;

  showNewFolderModal: boolean;
  setShowNewFolderModal: (open: boolean) => void;
  newFolderName: string;
  setNewFolderName: (name: string) => void;
  submitNewFolder: (e: React.FormEvent) => void;

  editingItem: FileItem | null;
  setEditingItem: (item: FileItem | null) => void;
  editName: string;
  setEditName: (name: string) => void;
  editTargetFolderId: string | null;
  setEditTargetFolderId: (id: string | null) => void;
  submitRenameMove: (e: React.FormEvent) => void;

  showBatchMoveModal: boolean;
  setShowBatchMoveModal: (open: boolean) => void;
  batchTargetFolderId: string | null;
  setBatchTargetFolderId: (id: string | null) => void;
  selectedIds: Set<string>;
  submitBatchMove: (e: React.FormEvent) => void;

  deleteConfirmIds: string[] | null;
  setDeleteConfirmIds: (ids: string[] | null) => void;
  confirmDelete: () => void;

  showCreateVolume: boolean;
  setShowCreateVolume: (open: boolean) => void;
  canAttachVolume: boolean;
  newVolume: { name: string; title: string };
  setNewVolume: React.Dispatch<React.SetStateAction<{ name: string; title: string }>>;
  volumeFormError: string;
  setVolumeFormError: (msg: string) => void;
  onCreateVolume: (name: string, title: string) => void;

  showAttachVolume: boolean;
  setShowAttachVolume: (open: boolean) => void;
  attachForm: { name: string; chatId: string };
  setAttachForm: React.Dispatch<React.SetStateAction<{ name: string; chatId: string }>>;
  attachFormError: string;
  setAttachFormError: (msg: string) => void;
  onAttachVolume: (name: string, chatID: number) => void;
  canListChannels: boolean;
  channels: ChannelOption[];
  channelsState: 'idle' | 'loading' | 'ready' | 'error';
  channelsError: string;
  loadChannels: () => void;
  pickChannel: (ch: ChannelOption) => void;

  showDeleteVolume: boolean;
  setShowDeleteVolume: (open: boolean) => void;
  deleteVolumeInput: string;
  setDeleteVolumeInput: (value: string) => void;
  onDeleteVolume: (name: string) => void;

  showRebuildConfirm: boolean;
  setShowRebuildConfirm: (open: boolean) => void;
  onRebuildVolume?: (volumeId: string) => void | Promise<void>;
  rebuilding: boolean;
  setRebuilding: (busy: boolean) => void;
  /** onNotifyError 把失败原因交给页面弹 toast；弹窗本身保持打开 */
  onNotifyError: (msg: string) => void;
}

// FilesDialogs 集中渲染文件页的八个弹窗。
//
// 输入：页面持有的表单状态与提交回调（状态仍留在 FilesView，弹窗只负责渲染）
// 输出：按各自的 open 条件挂载 native <dialog>
// 预期：关闭/取消都会把状态交回页面；危险操作（删除、重建）默认焦点落在取消上
export const FilesDialogs: React.FC<FilesDialogsProps> = ({
  t,
  locale,
  files,
  volumeDirectories,
  currentVolume,
  activeVolumeId,
  showNewFolderModal,
  setShowNewFolderModal,
  newFolderName,
  setNewFolderName,
  submitNewFolder,
  editingItem,
  setEditingItem,
  editName,
  setEditName,
  editTargetFolderId,
  setEditTargetFolderId,
  submitRenameMove,
  showBatchMoveModal,
  setShowBatchMoveModal,
  batchTargetFolderId,
  setBatchTargetFolderId,
  selectedIds,
  submitBatchMove,
  deleteConfirmIds,
  setDeleteConfirmIds,
  confirmDelete,
  showCreateVolume,
  setShowCreateVolume,
  canAttachVolume,
  newVolume,
  setNewVolume,
  volumeFormError,
  setVolumeFormError,
  onCreateVolume,
  showAttachVolume,
  setShowAttachVolume,
  attachForm,
  setAttachForm,
  attachFormError,
  setAttachFormError,
  onAttachVolume,
  canListChannels,
  channels,
  channelsState,
  channelsError,
  loadChannels,
  pickChannel,
  showDeleteVolume,
  setShowDeleteVolume,
  deleteVolumeInput,
  setDeleteVolumeInput,
  onDeleteVolume,
  showRebuildConfirm,
  setShowRebuildConfirm,
  onRebuildVolume,
  rebuilding,
  setRebuilding,
  onNotifyError,
}) => (
  <>
    {showNewFolderModal && (
      <Modal
        onClose={() => setShowNewFolderModal(false)}
        labelledBy="modal-new-folder-title"
        panelClassName="w-full max-w-md"
      >
        <form
          onSubmit={submitNewFolder}
          className="w-full max-w-md rounded-2xl surface-card shadow-2xl flex flex-col max-h-[calc(100dvh-2.5rem)] overflow-hidden"
        >
          <div className="p-5 sm:p-6 pb-3 shrink-0 border-b border-zinc-200/60 dark:border-zinc-800/60">
            <h3
              id="modal-new-folder-title"
              className="text-base font-semibold text-zinc-950 dark:text-white"
            >
              {t.files.modalNewFolderTitle}
            </h3>
          </div>
          <div className="flex-1 min-h-0 overflow-y-auto custom-scrollbar p-5 sm:p-6 space-y-4 overscroll-contain">
            <div className="space-y-1.5">
              <label
                htmlFor="new-folder-name-input"
                className="block text-xs font-medium text-zinc-700 dark:text-zinc-300"
              >
                {t.files.modalNewFolderLabel}
              </label>
              <Input
                id="new-folder-name-input"
                type="text"
                required
                autoFocus
                value={newFolderName}
                onChange={(e) => setNewFolderName(e.target.value)}
                placeholder={t.files.modalNewFolderPlaceholder}
              />
            </div>
          </div>
          <div className="p-4 sm:p-5 pt-3 shrink-0 border-t border-zinc-200/60 dark:border-zinc-800/60 flex items-center justify-end gap-2 bg-zinc-50/50 dark:bg-zinc-900/50">
            <Button type="button" variant="outline" onClick={() => setShowNewFolderModal(false)}>
              {t.files.cancel}
            </Button>
            <Button type="submit" variant="accent">
              {t.files.create}
            </Button>
          </div>
        </form>
      </Modal>
    )}

    {editingItem && (
      <Modal
        onClose={() => setEditingItem(null)}
        labelledBy="modal-rename-move-title"
        panelClassName="w-full max-w-md"
      >
        <form
          onSubmit={submitRenameMove}
          className="w-full max-w-md rounded-2xl surface-card shadow-2xl flex flex-col max-h-[calc(100dvh-2.5rem)] overflow-hidden"
        >
          <div className="p-5 sm:p-6 pb-3 shrink-0 border-b border-zinc-200/60 dark:border-zinc-800/60">
            <h3
              id="modal-rename-move-title"
              className="text-base font-semibold text-zinc-950 dark:text-white"
            >
              {t.files.modalRenameMoveTitle}
            </h3>
          </div>
          <div className="flex-1 min-h-0 overflow-y-auto custom-scrollbar p-5 sm:p-6 space-y-4 overscroll-contain">
            <div className="space-y-1.5">
              <label
                htmlFor="rename-item-input"
                className="block text-xs font-medium text-zinc-700 dark:text-zinc-300"
              >
                {t.files.modalRenameLabel}
              </label>
              <Input
                id="rename-item-input"
                type="text"
                required
                value={editName}
                aria-invalid={Boolean(
                  validateEntryName(editName, editingItem, editTargetFolderId, files, t)
                )}
                onChange={(e) => setEditName(e.target.value)}
              />
              {validateEntryName(editName, editingItem, editTargetFolderId, files, t) && (
                <p className="text-[11px] text-red-600 dark:text-red-400">
                  {validateEntryName(editName, editingItem, editTargetFolderId, files, t)}
                </p>
              )}
            </div>

            <div className="space-y-1.5">
              <span className="block text-xs font-medium text-zinc-700 dark:text-zinc-300">
                {t.files.moveTargetTree}
              </span>
              <FolderTree
                dirs={volumeDirectories}
                value={editTargetFolderId}
                onChange={setEditTargetFolderId}
                disabledIds={
                  new Set(
                    volumeDirectories
                      .filter((d) => !canMoveItemsToFolder(files, [editingItem.id], d.id))
                      .map((d) => d.id)
                  )
                }
                rootLabel={`/${currentVolume?.name ?? ''} (${t.files.rootCrumb})`}
                ariaLabel={t.files.modalMoveTargetLabel}
              />
              <p className="text-[11px] text-zinc-500 dark:text-zinc-400 font-mono" dir="ltr">
                {t.files.movePreview}:{' '}
                {joinPath(
                  targetPathText(files, editingItem.volumeId, editTargetFolderId),
                  editName.trim() || '…'
                )}
              </p>
            </div>
          </div>

          <div className="p-4 sm:p-5 pt-3 shrink-0 border-t border-zinc-200/60 dark:border-zinc-800/60 flex items-center justify-end gap-2 bg-zinc-50/50 dark:bg-zinc-900/50">
            <Button type="button" variant="outline" onClick={() => setEditingItem(null)}>
              {t.files.cancel}
            </Button>
            <Button type="submit" variant="accent">
              {t.files.save}
            </Button>
          </div>
        </form>
      </Modal>
    )}

    {showBatchMoveModal && (
      <Modal
        onClose={() => setShowBatchMoveModal(false)}
        labelledBy="modal-batch-move-title"
        panelClassName="w-full max-w-md"
      >
        <form
          onSubmit={submitBatchMove}
          className="w-full max-w-md rounded-2xl surface-card shadow-2xl flex flex-col max-h-[calc(100dvh-2.5rem)] overflow-hidden"
        >
          <div className="p-5 sm:p-6 pb-3 shrink-0 border-b border-zinc-200/60 dark:border-zinc-800/60">
            <h3
              id="modal-batch-move-title"
              className="text-base font-semibold text-zinc-950 dark:text-white"
            >
              {t.files.modalBatchMoveTitle}
            </h3>
          </div>
          <div className="flex-1 min-h-0 overflow-y-auto custom-scrollbar p-5 sm:p-6 space-y-4 overscroll-contain">
            <div className="space-y-1.5">
              <span className="block text-xs font-medium text-zinc-700 dark:text-zinc-300">
                {t.files.moveTargetTree}
              </span>
              <FolderTree
                dirs={volumeDirectories}
                value={batchTargetFolderId}
                onChange={setBatchTargetFolderId}
                disabledIds={
                  new Set(
                    volumeDirectories
                      .filter((d) => !canMoveItemsToFolder(files, Array.from(selectedIds), d.id))
                      .map((d) => d.id)
                  )
                }
                rootLabel={`/${currentVolume?.name ?? ''} (${t.files.rootCrumb})`}
                ariaLabel={t.files.modalMoveTargetLabel}
              />
              <p className="text-[11px] text-zinc-500 dark:text-zinc-400 font-mono" dir="ltr">
                {t.files.movePreview}: {targetPathText(files, activeVolumeId, batchTargetFolderId)} ·{' '}
                {formatPlural(selectedIds.size, t.plurals.selectedItems, locale)}
              </p>
            </div>
          </div>
          <div className="p-4 sm:p-5 pt-3 shrink-0 border-t border-zinc-200/60 dark:border-zinc-800/60 flex items-center justify-end gap-2 bg-zinc-50/50 dark:bg-zinc-900/50">
            <Button type="button" variant="outline" onClick={() => setShowBatchMoveModal(false)}>
              {t.files.cancel}
            </Button>
            <Button type="submit" variant="accent">
              {t.files.confirm}
            </Button>
          </div>
        </form>
      </Modal>
    )}

    {deleteConfirmIds && deleteConfirmIds.length > 0 && (
      <Modal
        onClose={() => setDeleteConfirmIds(null)}
        labelledBy="modal-delete-confirm-title"
        panelClassName="w-full max-w-md"
      >
        <div className="w-full max-w-md rounded-2xl surface-card shadow-2xl flex flex-col max-h-[calc(100dvh-2.5rem)] overflow-hidden">
          <div className="p-5 sm:p-6 pb-3 shrink-0 border-b border-zinc-200/60 dark:border-zinc-800/60">
            <h3
              id="modal-delete-confirm-title"
              className="text-base font-semibold text-zinc-950 dark:text-white"
            >
              {t.files.modalConfirmDeleteTitle}
            </h3>
          </div>
          <div className="flex-1 min-h-0 overflow-y-auto custom-scrollbar p-5 sm:p-6 space-y-4 overscroll-contain">
            <p className="text-xs text-zinc-600 dark:text-zinc-400 leading-relaxed">
              {deleteConfirmIds.length === 1
                ? interpolate(
                    t.files.modalConfirmDeleteDesc,
                    { name: files.find((f) => f.id === deleteConfirmIds[0])?.name || '' },
                    locale
                  )
                : formatPlural(deleteConfirmIds.length, t.plurals.batchDeleteConfirm, locale)}
            </p>
          </div>
          <div className="p-4 sm:p-5 pt-3 shrink-0 border-t border-zinc-200/60 dark:border-zinc-800/60 flex items-center justify-end gap-2 bg-zinc-50/50 dark:bg-zinc-900/50">
            <Button
              type="button"
              variant="outline"
              autoFocus
              onClick={() => setDeleteConfirmIds(null)}
            >
              {t.files.cancel}
            </Button>
            <Button type="button" variant="destructive" onClick={confirmDelete}>
              {t.files.deleteConfirmBtn}
            </Button>
          </div>
        </div>
      </Modal>
    )}

    {(showCreateVolume || showAttachVolume) && (() => {
      const isCreate = showCreateVolume;
      const closeAll = () => {
        setShowCreateVolume(false);
        setShowAttachVolume(false);
      };
      return (
        <Modal onClose={closeAll} labelledBy="modal-volume-title" panelClassName="w-full max-w-md">
          <form
            onSubmit={(e) => {
              e.preventDefault();
              if (isCreate) {
                const name = newVolume.name.trim();
                if (!/^[A-Za-z0-9_-]{1,32}$/.test(name)) {
                  setVolumeFormError(t.volume.invalidName);
                  return;
                }
                onCreateVolume(name, newVolume.title.trim());
              } else {
                const name = attachForm.name.trim();
                const id = Number(attachForm.chatId.trim());
                if (!/^[A-Za-z0-9_-]{1,32}$/.test(name)) {
                  setAttachFormError(t.volume.invalidName);
                  return;
                }
                if (!Number.isInteger(id) || id === 0) {
                  setAttachFormError(t.volume.chatIdInvalid);
                  return;
                }
                onAttachVolume(name, id);
              }
              closeAll();
            }}
            className="w-full max-w-md rounded-2xl surface-card shadow-2xl flex flex-col max-h-[calc(100dvh-2.5rem)] overflow-hidden"
          >
            <div className="p-5 sm:p-6 pb-3 shrink-0 border-b border-zinc-200/60 dark:border-zinc-800/60 space-y-1">
              <h3
                id="modal-volume-title"
                className="text-base font-semibold text-zinc-950 dark:text-white"
              >
                {isCreate ? t.volume.createTitle : t.volume.attachTitle}
              </h3>
              <p className="text-xs text-zinc-500 dark:text-zinc-400">
                {isCreate ? t.volume.createDesc : t.volume.attachDesc}
              </p>
              {/* 二选一：新建频道 / 添加已有频道，同一弹窗内切换 */}
              <div role="tablist" aria-label={t.volume.createTitle} className="flex gap-2 pt-1">
                <button
                  type="button"
                  role="tab"
                  aria-selected={isCreate}
                  onClick={() => {
                    setNewVolume({ name: '', title: '' });
                    setVolumeFormError('');
                    setShowAttachVolume(false);
                    setShowCreateVolume(true);
                  }}
                  className={`px-3 py-1.5 rounded-lg text-xs font-medium transition-colors ${
                    isCreate
                      ? 'bg-sky-50 text-sky-700 dark:bg-sky-500/10 dark:text-sky-300'
                      : 'text-zinc-600 hover:bg-zinc-100 dark:text-zinc-300 dark:hover:bg-zinc-800/60'
                  }`}
                >
                  {t.volume.createBtn}
                </button>
                {canAttachVolume && (
                  <button
                    type="button"
                    role="tab"
                    aria-selected={!isCreate}
                    onClick={() => {
                      setAttachForm({ name: '', chatId: '' });
                      setAttachFormError('');
                      setShowCreateVolume(false);
                      setShowAttachVolume(true);
                    }}
                    className={`px-3 py-1.5 rounded-lg text-xs font-medium transition-colors ${
                      !isCreate
                        ? 'bg-sky-50 text-sky-700 dark:bg-sky-500/10 dark:text-sky-300'
                        : 'text-zinc-600 hover:bg-zinc-100 dark:text-zinc-300 dark:hover:bg-zinc-800/60'
                    }`}
                  >
                    {t.volume.attachBtn}
                  </button>
                )}
              </div>
            </div>
            <div className="flex-1 min-h-0 overflow-y-auto custom-scrollbar p-5 sm:p-6 space-y-4 overscroll-contain">
              {isCreate ? (
                <>
                  <div className="space-y-1.5">
                    <label htmlFor="volume-name" className="block text-xs font-medium">
                      {t.volume.nameLabel}
                    </label>
                    <Input
                      id="volume-name"
                      required
                      autoFocus
                      dir="ltr"
                      pattern="[A-Za-z0-9_-]{1,32}"
                      aria-invalid={volumeFormError ? true : undefined}
                      aria-describedby="volume-name-hint"
                      value={newVolume.name}
                      onChange={(e) => {
                        setVolumeFormError('');
                        setNewVolume((v) => ({ ...v, name: e.target.value }));
                      }}
                    />
                    <p id="volume-name-hint" className="text-[11px] text-zinc-500 dark:text-zinc-400">
                      {t.volume.nameHint}
                    </p>
                    {volumeFormError && (
                      <p role="alert" className="text-[11px] text-red-600 dark:text-red-400">
                        {volumeFormError}
                      </p>
                    )}
                  </div>
                  <div className="space-y-1.5">
                    <label htmlFor="volume-title" className="block text-xs font-medium">
                      {t.volume.titleLabel}
                    </label>
                    <Input
                      id="volume-title"
                      value={newVolume.title}
                      onChange={(e) => setNewVolume((v) => ({ ...v, title: e.target.value }))}
                    />
                  </div>
                </>
              ) : (
                <>
                  {canListChannels && (
                    <div className="space-y-2">
                      <div className="flex flex-wrap items-center justify-between gap-2">
                        <span className="text-xs font-medium">{t.volume.pickChannel}</span>
                        <Button
                          type="button"
                          variant="outline"
                          onClick={loadChannels}
                          disabled={channelsState === 'loading'}
                        >
                          {channelsState === 'loading' ? (
                            <Loader2 className="w-3.5 h-3.5 animate-spin" />
                          ) : (
                            <List className="w-3.5 h-3.5" />
                          )}
                          <span>{t.volume.loadChannels}</span>
                        </Button>
                      </div>
                      {channelsState === 'error' && (
                        <p className="text-[11px] text-rose-500 break-words">
                          {t.volume.channelsFailed}: {channelsError}
                        </p>
                      )}
                      {channelsState === 'ready' && channels.length === 0 && (
                        <p className="text-[11px] text-zinc-500 dark:text-zinc-400">
                          {t.volume.channelsEmpty}
                        </p>
                      )}
                      {channels.length > 0 && (
                        <div
                          className="max-h-40 overflow-y-auto rounded-lg border border-zinc-200 dark:border-zinc-800 divide-y divide-zinc-200 dark:divide-zinc-800 custom-scrollbar"
                          role="listbox"
                          aria-label={t.volume.pickChannel}
                        >
                          {channels.map((ch) => (
                            <button
                              key={ch.id}
                              type="button"
                              role="option"
                              aria-selected={String(ch.chat_id) === attachForm.chatId}
                              onClick={() => pickChannel(ch)}
                              className={`w-full text-left px-3 py-2 transition-colors ${
                                String(ch.chat_id) === attachForm.chatId
                                  ? 'bg-sky-50 dark:bg-sky-500/10'
                                  : 'hover:bg-zinc-50 dark:hover:bg-zinc-800/60'
                              }`}
                            >
                              <span className="block text-xs font-medium truncate text-zinc-900 dark:text-zinc-100">
                                {ch.title || ch.username || String(ch.id)}
                              </span>
                              <span
                                className="block text-[11px] text-zinc-500 dark:text-zinc-400 truncate"
                                dir="ltr"
                              >
                                {ch.chat_id}
                                {ch.username ? ` · @${ch.username}` : ''}
                                {ch.creator ? ' · owner' : ch.admin ? ' · admin' : ''}
                              </span>
                            </button>
                          ))}
                        </div>
                      )}
                    </div>
                  )}

                  <div className="space-y-1.5">
                    <label htmlFor="attach-volume-name" className="block text-xs font-medium">
                      {t.volume.nameLabel}
                    </label>
                    <Input
                      id="attach-volume-name"
                      required
                      autoFocus
                      dir="ltr"
                      pattern="[A-Za-z0-9_-]{1,32}"
                      aria-invalid={attachFormError ? true : undefined}
                      aria-describedby="attach-volume-name-hint"
                      value={attachForm.name}
                      onChange={(e) => {
                        setAttachFormError('');
                        setAttachForm((v) => ({ ...v, name: e.target.value }));
                      }}
                    />
                    <p id="attach-volume-name-hint" className="text-[11px] text-zinc-500 dark:text-zinc-400">
                      {t.volume.nameHint}
                    </p>
                  </div>
                  <div className="space-y-1.5">
                    <label htmlFor="attach-volume-chat" className="block text-xs font-medium">
                      {t.volume.chatIdLabel}
                    </label>
                    <Input
                      id="attach-volume-chat"
                      required
                      dir="ltr"
                      inputMode="numeric"
                      aria-invalid={attachFormError ? true : undefined}
                      aria-describedby="attach-volume-chat-hint"
                      value={attachForm.chatId}
                      onChange={(e) => {
                        setAttachFormError('');
                        setAttachForm((v) => ({ ...v, chatId: e.target.value }));
                      }}
                    />
                    <p id="attach-volume-chat-hint" className="text-[11px] text-zinc-500 dark:text-zinc-400">
                      {t.volume.chatIdHint}
                    </p>
                    {attachFormError && (
                      <p role="alert" className="text-[11px] text-red-600 dark:text-red-400">
                        {attachFormError}
                      </p>
                    )}
                  </div>
                </>
              )}
            </div>
            <div className="p-4 sm:p-5 pt-3 shrink-0 border-t border-zinc-200/60 dark:border-zinc-800/60 flex items-center justify-end gap-2 bg-zinc-50/50 dark:bg-zinc-900/50">
              <Button type="button" variant="outline" onClick={closeAll}>
                {t.files.cancel}
              </Button>
              <Button type="submit" variant="accent">
                {isCreate ? t.volume.createConfirm : t.volume.attachConfirm}
              </Button>
            </div>
          </form>
        </Modal>
      );
    })()}

    {showDeleteVolume && currentVolume && (
      <Modal
        onClose={() => {
          setShowDeleteVolume(false);
          setDeleteVolumeInput('');
        }}
        labelledBy="modal-delete-volume-title"
        panelClassName="w-full max-w-md"
      >
        <div className="w-full max-w-md rounded-2xl surface-card shadow-2xl flex flex-col max-h-[calc(100dvh-2.5rem)] overflow-hidden">
          <div className="p-5 sm:p-6 pb-3 shrink-0 border-b border-zinc-200/60 dark:border-zinc-800/60 flex items-start gap-3">
            <div className="w-9 h-9 rounded-xl bg-red-500/10 text-red-600 dark:text-red-400 flex items-center justify-center shrink-0">
              <AlertTriangle className="w-5 h-5" />
            </div>
            <div>
              <h3
                id="modal-delete-volume-title"
                className="text-base font-semibold text-zinc-950 dark:text-white"
              >
                {t.volume.deleteConfirmTitle}
              </h3>
              <p className="text-xs text-zinc-600 dark:text-zinc-400 leading-relaxed mt-1">
                {interpolate(t.volume.deleteConfirmDesc, { name: currentVolume.name }, locale)}
              </p>
            </div>
          </div>
          <div className="flex-1 min-h-0 overflow-y-auto custom-scrollbar p-5 sm:p-6 space-y-4 overscroll-contain">
            <div className="space-y-1.5">
              <label htmlFor="delete-volume-name" className="block text-xs font-medium">
                {interpolate(t.volume.typeToConfirm, { name: currentVolume.name }, locale)}
              </label>
              <Input
                id="delete-volume-name"
                dir="ltr"
                autoFocus
                value={deleteVolumeInput}
                onChange={(e) => setDeleteVolumeInput(e.target.value)}
                placeholder={currentVolume.name}
              />
            </div>
          </div>
          <div className="p-4 sm:p-5 pt-3 shrink-0 border-t border-zinc-200/60 dark:border-zinc-800/60 flex items-center justify-end gap-2 bg-zinc-50/50 dark:bg-zinc-900/50">
            <Button
              type="button"
              variant="outline"
              onClick={() => {
                setShowDeleteVolume(false);
                setDeleteVolumeInput('');
              }}
            >
              {t.files.cancel}
            </Button>
            <Button
              type="button"
              variant="destructive"
              disabled={deleteVolumeInput.trim() !== currentVolume.name}
              onClick={() => {
                onDeleteVolume(currentVolume.name);
                setShowDeleteVolume(false);
                setDeleteVolumeInput('');
              }}
            >
              {t.volume.deleteConfirm}
            </Button>
          </div>
        </div>
      </Modal>
    )}

    {showRebuildConfirm && onRebuildVolume && (
      <Modal
        onClose={() => setShowRebuildConfirm(false)}
        labelledBy="modal-rebuild-title"
        panelClassName="w-full max-w-md"
      >
        <div className="w-full max-w-md rounded-2xl surface-card shadow-2xl flex flex-col max-h-[calc(100dvh-2.5rem)] overflow-hidden">
          <div className="p-5 sm:p-6 pb-3 shrink-0 border-b border-zinc-200/60 dark:border-zinc-800/60">
            <h3
              id="modal-rebuild-title"
              className="text-base font-semibold text-zinc-950 dark:text-white"
            >
              {t.files.rebuildConfirmTitle}
            </h3>
          </div>
          <div className="flex-1 min-h-0 overflow-y-auto custom-scrollbar p-5 sm:p-6 space-y-4 overscroll-contain">
            <p className="text-xs text-zinc-600 dark:text-zinc-400 leading-relaxed">
              {t.files.rebuildConfirmDesc}
            </p>
          </div>
          <div className="p-4 sm:p-5 pt-3 shrink-0 border-t border-zinc-200/60 dark:border-zinc-800/60 flex items-center justify-end gap-2 bg-zinc-50/50 dark:bg-zinc-900/50">
            <Button
              type="button"
              variant="outline"
              disabled={rebuilding}
              onClick={() => setShowRebuildConfirm(false)}
            >
              {t.files.cancel}
            </Button>
            <Button
              type="button"
              variant="destructive"
              disabled={rebuilding}
              onClick={async () => {
                setRebuilding(true);
                try {
                  await onRebuildVolume(activeVolumeId);
                  setShowRebuildConfirm(false);
                } catch (err) {
                  // 失败时保留弹窗，让用户看得见是哪一步出错
                  onNotifyError(err instanceof Error ? err.message : String(err));
                } finally {
                  setRebuilding(false);
                }
              }}
            >
              {t.files.rebuildConfirm}
            </Button>
          </div>
        </div>
      </Modal>
    )}
  </>
);
