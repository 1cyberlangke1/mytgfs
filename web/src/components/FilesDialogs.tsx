import React from 'react';
import { AlertTriangle, List, Loader2 } from 'lucide-react';
import type { FileItem, Locale, ManageableChannel, VolumeInfo } from '../types';
import { canMoveItemsToFolder } from '../utils/fsLogic';
import { formatPlural, interpolate, type Translations } from '../utils/i18n';
import { errText } from '../utils/errorText';
import { FolderTree } from './FolderTree';
import { joinPath, targetPathText, validateEntryName } from './filesHelpers';
import { Modal } from './Modal';
import { Button, Input } from './ui';

/** ChannelOption 是「添加已有频道」列表里的一项。 */
type ChannelOption = ManageableChannel;

/** 面板容器：统一圆角、卡片底色、最大高度与纵向布局 */
const PANEL_CLASS =
  'w-full max-w-md rounded-2xl surface-card shadow-2xl flex flex-col max-h-[calc(100dvh-2.5rem)] overflow-hidden';
const HEADER_CLASS = 'p-5 sm:p-6 pb-3 shrink-0 border-b border-zinc-200/60 dark:border-zinc-800/60';
const BODY_CLASS =
  'flex-1 min-h-0 overflow-y-auto custom-scrollbar p-5 sm:p-6 space-y-4 overscroll-contain';
const FOOTER_CLASS =
  'p-4 sm:p-5 pt-3 shrink-0 border-t border-zinc-200/60 dark:border-zinc-800/60 flex items-center justify-end gap-2 bg-zinc-50/50 dark:bg-zinc-900/50';

// 弹窗三段布局：头/身/脚；图标只用于危险操作。
function Headline({
  id,
  title,
  description,
  icon,
  children,
}: {
  id: string;
  title: string;
  description?: string;
  icon?: React.ReactNode;
  children?: React.ReactNode;
}) {
  const heading = (
    <div>
      <h3 id={id} className="text-base font-semibold text-zinc-950 dark:text-white">
        {title}
      </h3>
      {description && (
        <p className="text-xs text-zinc-600 dark:text-zinc-400 leading-relaxed mt-1">{description}</p>
      )}
      {children}
    </div>
  );
  return icon ? (
    <div className={`${HEADER_CLASS} flex items-start gap-3`}>
      <div className="w-9 h-9 rounded-xl bg-red-500/10 text-red-600 dark:text-red-400 flex items-center justify-center shrink-0">
        {icon}
      </div>
      {heading}
    </div>
  ) : (
    <div className={HEADER_CLASS}>{heading}</div>
  );
}

const Body = ({ children }: { children: React.ReactNode }) => <div className={BODY_CLASS}>{children}</div>;
const Footer = ({ children }: { children: React.ReactNode }) => <div className={FOOTER_CLASS}>{children}</div>;
const Label = ({ htmlFor, children }: { htmlFor?: string; children: React.ReactNode }) => (
  <label htmlFor={htmlFor} className="block text-xs font-medium text-zinc-700 dark:text-zinc-300">
    {children}
  </label>
);
const Hint = ({ id, children }: { id?: string; children: React.ReactNode }) => (
  <p id={id} className="text-[11px] text-zinc-500 dark:text-zinc-400">
    {children}
  </p>
);
const ErrorText = ({ children }: { children: React.ReactNode }) => (
  <p role="alert" className="text-[11px] text-red-600 dark:text-red-400">
    {children}
  </p>
);

interface FilesDialogsProps {
  t: Translations;
  locale: Locale;
  files: FileItem[];
  volumeDirectories: FileItem[];
  currentVolume?: VolumeInfo;
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
  canAttachVolume: boolean;
  setShowCreateVolume: (open: boolean) => void;
  newVolume: { name: string; title: string };
  setNewVolume: (v: { name: string; title: string }) => void;
  volumeFormError: string;
  setVolumeFormError: (msg: string) => void;
  onCreateVolume: (name: string, title: string) => void;
  showAttachVolume: boolean;
  setShowAttachVolume: (open: boolean) => void;
  attachForm: { name: string; chatId: string };
  setAttachForm: (v: { name: string; chatId: string }) => void;
  attachFormError: string;
  setAttachFormError: (msg: string) => void;
  onAttachVolume: (name: string, chatId: number) => void;
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
  onRebuildVolume?: (volumeId: string) => Promise<void>;
  rebuilding: boolean;
  setRebuilding: (busy: boolean) => void;
  onNotifyError: (msg: string) => void;
}

/** 卷名规则：字母/数字/下划线/连字符，1~32 位；与后端 config.ValidVolumeName 一致 */
const VOLUME_NAME_RE = /^[A-Za-z0-9_-]{1,32}$/;

export const FilesDialogs: React.FC<FilesDialogsProps> = (p) => {
  const { t, locale, files, volumeDirectories, currentVolume } = p;
  const closeVolume = () => {
    p.setShowCreateVolume(false);
    p.setShowAttachVolume(false);
  };
  // 建卷/挂卷共用一个弹窗：切 tab 时重置另一侧的表单与错误
  const switchVolume = (mode: 'create' | 'attach') => {
    if (mode === 'create') {
      p.setNewVolume({ name: '', title: '' });
      p.setVolumeFormError('');
      p.setShowAttachVolume(false);
      p.setShowCreateVolume(true);
      return;
    }
    p.setAttachForm({ name: '', chatId: '' });
    p.setAttachFormError('');
    p.setShowCreateVolume(false);
    p.setShowAttachVolume(true);
  };
  const submitVolume = (e: React.FormEvent) => {
    e.preventDefault();
    if (p.showCreateVolume) {
      const name = p.newVolume.name.trim();
      if (!VOLUME_NAME_RE.test(name)) return p.setVolumeFormError(t.volume.invalidName);
      p.onCreateVolume(name, p.newVolume.title.trim());
    } else {
      const name = p.attachForm.name.trim();
      const id = Number(p.attachForm.chatId.trim());
      if (!VOLUME_NAME_RE.test(name)) return p.setAttachFormError(t.volume.invalidName);
      if (!Number.isInteger(id) || id === 0) return p.setAttachFormError(t.volume.chatIdInvalid);
      p.onAttachVolume(name, id);
    }
    closeVolume();
  };
  const deleteError = p.editingItem
    ? validateEntryName(p.editName, p.editingItem, p.editTargetFolderId, files, t)
    : null;
  const rootLabel = `/${currentVolume?.name ?? ''} (${t.files.rootCrumb})`;
  const deleteDescription =
    p.deleteConfirmIds && p.deleteConfirmIds.length > 0
      ? p.deleteConfirmIds.length === 1
        ? interpolate(
            t.files.modalConfirmDeleteDesc,
            { name: files.find((f) => f.id === p.deleteConfirmIds![0])?.name || '' },
            locale
          )
        : formatPlural(p.deleteConfirmIds.length, t.plurals.batchDeleteConfirm, locale)
      : '';

  return (
    <>
      {p.showNewFolderModal && (
        <Modal onClose={() => p.setShowNewFolderModal(false)} labelledBy="modal-new-folder-title" panelClassName="w-full max-w-md">
          <form onSubmit={p.submitNewFolder} className={PANEL_CLASS}>
            <Headline id="modal-new-folder-title" title={t.files.modalNewFolderTitle} />
            <Body>
              <div className="space-y-1.5">
                <Label htmlFor="new-folder-name-input">{t.files.modalNewFolderLabel}</Label>
                <Input
                  id="new-folder-name-input"
                  type="text"
                  required
                  autoFocus
                  value={p.newFolderName}
                  onChange={(e) => p.setNewFolderName(e.target.value)}
                  placeholder={t.files.modalNewFolderPlaceholder}
                />
              </div>
            </Body>
            <Footer>
              <Button type="button" variant="outline" onClick={() => p.setShowNewFolderModal(false)}>
                {t.files.cancel}
              </Button>
              <Button type="submit" variant="accent">
                {t.files.create}
              </Button>
            </Footer>
          </form>
        </Modal>
      )}

      {p.editingItem && (
        <Modal onClose={() => p.setEditingItem(null)} labelledBy="modal-rename-move-title" panelClassName="w-full max-w-md">
          <form onSubmit={p.submitRenameMove} className={PANEL_CLASS}>
            <Headline id="modal-rename-move-title" title={t.files.modalRenameMoveTitle} />
            <Body>
              <div className="space-y-1.5">
                <Label htmlFor="rename-item-input">{t.files.modalRenameLabel}</Label>
                <Input
                  id="rename-item-input"
                  type="text"
                  required
                  value={p.editName}
                  aria-invalid={Boolean(deleteError)}
                  onChange={(e) => p.setEditName(e.target.value)}
                />
                {deleteError && <ErrorText>{deleteError}</ErrorText>}
              </div>
              <div className="space-y-1.5">
                <span className="block text-xs font-medium text-zinc-700 dark:text-zinc-300">
                  {t.files.moveTargetTree}
                </span>
                <FolderTree
                  dirs={volumeDirectories}
                  value={p.editTargetFolderId}
                  onChange={p.setEditTargetFolderId}
                  disabledIds={
                    new Set(
                      volumeDirectories
                        .filter((d) => !canMoveItemsToFolder(files, [p.editingItem!.id], d.id))
                        .map((d) => d.id)
                    )
                  }
                  rootLabel={rootLabel}
                  ariaLabel={t.files.modalMoveTargetLabel}
                />
                <p className="text-[11px] text-zinc-500 dark:text-zinc-400 font-mono" dir="ltr">
                  {t.files.movePreview}:{' '}
                  {joinPath(targetPathText(files, p.editingItem.volumeId, p.editTargetFolderId), p.editName.trim() || '…')}
                </p>
              </div>
            </Body>
            <Footer>
              <Button type="button" variant="outline" onClick={() => p.setEditingItem(null)}>
                {t.files.cancel}
              </Button>
              <Button type="submit" variant="accent">
                {t.files.save}
              </Button>
            </Footer>
          </form>
        </Modal>
      )}

      {p.showBatchMoveModal && (
        <Modal onClose={() => p.setShowBatchMoveModal(false)} labelledBy="modal-batch-move-title" panelClassName="w-full max-w-md">
          <form onSubmit={p.submitBatchMove} className={PANEL_CLASS}>
            <Headline id="modal-batch-move-title" title={t.files.modalBatchMoveTitle} />
            <Body>
              <div className="space-y-1.5">
                <span className="block text-xs font-medium text-zinc-700 dark:text-zinc-300">
                  {t.files.moveTargetTree}
                </span>
                <FolderTree
                  dirs={volumeDirectories}
                  value={p.batchTargetFolderId}
                  onChange={p.setBatchTargetFolderId}
                  disabledIds={
                    new Set(
                      volumeDirectories
                        .filter((d) => !canMoveItemsToFolder(files, Array.from(p.selectedIds), d.id))
                        .map((d) => d.id)
                    )
                  }
                  rootLabel={rootLabel}
                  ariaLabel={t.files.modalMoveTargetLabel}
                />
                <p className="text-[11px] text-zinc-500 dark:text-zinc-400 font-mono" dir="ltr">
                  {t.files.movePreview}: {targetPathText(files, p.activeVolumeId, p.batchTargetFolderId)} ·{' '}
                  {formatPlural(p.selectedIds.size, t.plurals.selectedItems, locale)}
                </p>
              </div>
            </Body>
            <Footer>
              <Button type="button" variant="outline" onClick={() => p.setShowBatchMoveModal(false)}>
                {t.files.cancel}
              </Button>
              <Button type="submit" variant="accent">
                {t.files.confirm}
              </Button>
            </Footer>
          </form>
        </Modal>
      )}

      {p.deleteConfirmIds && p.deleteConfirmIds.length > 0 && (
        <Modal onClose={() => p.setDeleteConfirmIds(null)} labelledBy="modal-delete-confirm-title" panelClassName="w-full max-w-md">
          <div className={PANEL_CLASS}>
            <Headline id="modal-delete-confirm-title" title={t.files.modalConfirmDeleteTitle} />
            <Body>
              <p className="text-xs text-zinc-600 dark:text-zinc-400 leading-relaxed">{deleteDescription}</p>
            </Body>
            <Footer>
              <Button type="button" variant="outline" autoFocus onClick={() => p.setDeleteConfirmIds(null)}>
                {t.files.cancel}
              </Button>
              <Button type="button" variant="destructive" onClick={p.confirmDelete}>
                {t.files.deleteConfirmBtn}
              </Button>
            </Footer>
          </div>
        </Modal>
      )}

      {(p.showCreateVolume || p.showAttachVolume) && (
        <Modal onClose={closeVolume} labelledBy="modal-volume-title" panelClassName="w-full max-w-md">
          <form onSubmit={submitVolume} className={PANEL_CLASS}>
            <div className="p-5 sm:p-6 pb-3 shrink-0 border-b border-zinc-200/60 dark:border-zinc-800/60 space-y-1">
              <h3 id="modal-volume-title" className="text-base font-semibold text-zinc-950 dark:text-white">
                {p.showCreateVolume ? t.volume.createTitle : t.volume.attachTitle}
              </h3>
              <p className="text-xs text-zinc-500 dark:text-zinc-400">
                {p.showCreateVolume ? t.volume.createDesc : t.volume.attachDesc}
              </p>
              <div role="tablist" aria-label={t.volume.createTitle} className="flex gap-2 pt-1">
                {(
                  [
                    ['create', t.volume.createBtn, true],
                    ['attach', t.volume.attachBtn, p.canAttachVolume],
                  ] as const
                ).map(([mode, label, shown]) =>
                  shown ? (
                    <button
                      key={mode}
                      type="button"
                      role="tab"
                      aria-selected={p.showCreateVolume === (mode === 'create')}
                      onClick={() => switchVolume(mode)}
                      className={`px-3 py-1.5 rounded-lg text-xs font-medium transition-colors ${
                        p.showCreateVolume === (mode === 'create')
                          ? 'bg-sky-50 text-sky-700 dark:bg-sky-500/10 dark:text-sky-300'
                          : 'text-zinc-600 hover:bg-zinc-100 dark:text-zinc-300 dark:hover:bg-zinc-800/60'
                      }`}
                    >
                      {label}
                    </button>
                  ) : null
                )}
              </div>
            </div>

            <Body>
              {p.showCreateVolume ? (
                <>
                  <div className="space-y-1.5">
                    <Label htmlFor="volume-name">{t.volume.nameLabel}</Label>
                    <Input
                      id="volume-name"
                      required
                      autoFocus
                      dir="ltr"
                      pattern="[A-Za-z0-9_-]{1,32}"
                      aria-invalid={p.volumeFormError ? true : undefined}
                      aria-describedby="volume-name-hint"
                      value={p.newVolume.name}
                      onChange={(e) => {
                        p.setVolumeFormError('');
                        p.setNewVolume({ ...p.newVolume, name: e.target.value });
                      }}
                    />
                    <Hint id="volume-name-hint">{t.volume.nameHint}</Hint>
                    {p.volumeFormError && <ErrorText>{p.volumeFormError}</ErrorText>}
                  </div>
                  <div className="space-y-1.5">
                    <Label htmlFor="volume-title">{t.volume.titleLabel}</Label>
                    <Input
                      id="volume-title"
                      value={p.newVolume.title}
                      onChange={(e) => p.setNewVolume({ ...p.newVolume, title: e.target.value })}
                    />
                  </div>
                </>
              ) : (
                <>
                  {p.canListChannels && (
                    <div className="space-y-2">
                      <div className="flex flex-wrap items-center justify-between gap-2">
                        <span className="text-xs font-medium">{t.volume.pickChannel}</span>
                        <Button type="button" variant="outline" onClick={p.loadChannels} disabled={p.channelsState === 'loading'}>
                          {p.channelsState === 'loading' ? (
                            <Loader2 className="w-3.5 h-3.5 animate-spin" />
                          ) : (
                            <List className="w-3.5 h-3.5" />
                          )}
                          <span>{t.volume.loadChannels}</span>
                        </Button>
                      </div>
                      {p.channelsState === 'error' && (
                        <p className="text-[11px] text-rose-500 break-words">
                          {t.volume.channelsFailed}: {p.channelsError}
                        </p>
                      )}
                      {p.channelsState === 'ready' && p.channels.length === 0 && <Hint>{t.volume.channelsEmpty}</Hint>}
                      {p.channels.length > 0 && (
                        <div
                          className="max-h-40 overflow-y-auto rounded-lg border border-zinc-200 dark:border-zinc-800 divide-y divide-zinc-200 dark:divide-zinc-800 custom-scrollbar"
                          role="listbox"
                          aria-label={t.volume.pickChannel}
                        >
                          {p.channels.map((ch) => {
                            const active = String(ch.chat_id) === p.attachForm.chatId;
                            return (
                              <button
                                key={ch.id}
                                type="button"
                                role="option"
                                aria-selected={active}
                                onClick={() => p.pickChannel(ch)}
                                className={`w-full text-left px-3 py-2 transition-colors ${
                                  active ? 'bg-sky-50 dark:bg-sky-500/10' : 'hover:bg-zinc-50 dark:hover:bg-zinc-800/60'
                                }`}
                              >
                                <span className="block text-xs font-medium truncate text-zinc-900 dark:text-zinc-100">
                                  {ch.title || ch.username || String(ch.id)}
                                </span>
                                <span className="block text-[11px] text-zinc-500 dark:text-zinc-400 truncate" dir="ltr">
                                  {ch.chat_id}
                                  {ch.username ? ` · @${ch.username}` : ''}
                                  {ch.creator ? ' · owner' : ch.admin ? ' · admin' : ''}
                                </span>
                              </button>
                            );
                          })}
                        </div>
                      )}
                    </div>
                  )}
                  <div className="space-y-1.5">
                    <Label htmlFor="attach-volume-name">{t.volume.nameLabel}</Label>
                    <Input
                      id="attach-volume-name"
                      required
                      autoFocus
                      dir="ltr"
                      pattern="[A-Za-z0-9_-]{1,32}"
                      aria-invalid={p.attachFormError ? true : undefined}
                      aria-describedby="attach-volume-name-hint"
                      value={p.attachForm.name}
                      onChange={(e) => {
                        p.setAttachFormError('');
                        p.setAttachForm({ ...p.attachForm, name: e.target.value });
                      }}
                    />
                    <Hint id="attach-volume-name-hint">{t.volume.nameHint}</Hint>
                  </div>
                  <div className="space-y-1.5">
                    <Label htmlFor="attach-volume-chat">{t.volume.chatIdLabel}</Label>
                    <Input
                      id="attach-volume-chat"
                      required
                      dir="ltr"
                      inputMode="numeric"
                      aria-invalid={p.attachFormError ? true : undefined}
                      aria-describedby="attach-volume-chat-hint"
                      value={p.attachForm.chatId}
                      onChange={(e) => {
                        p.setAttachFormError('');
                        p.setAttachForm({ ...p.attachForm, chatId: e.target.value });
                      }}
                    />
                    <Hint id="attach-volume-chat-hint">{t.volume.chatIdHint}</Hint>
                    {p.attachFormError && <ErrorText>{p.attachFormError}</ErrorText>}
                  </div>
                </>
              )}
            </Body>

            <Footer>
              <Button type="button" variant="outline" onClick={closeVolume}>
                {t.files.cancel}
              </Button>
              <Button type="submit" variant="accent">
                {p.showCreateVolume ? t.volume.createConfirm : t.volume.attachConfirm}
              </Button>
            </Footer>
          </form>
        </Modal>
      )}

      {p.showDeleteVolume && currentVolume && (
        <Modal onClose={() => { p.setShowDeleteVolume(false); p.setDeleteVolumeInput(''); }} labelledBy="modal-delete-volume-title" panelClassName="w-full max-w-md">
          <div className={PANEL_CLASS}>
            <Headline
              id="modal-delete-volume-title"
              title={t.volume.deleteConfirmTitle}
              description={interpolate(t.volume.deleteConfirmDesc, { name: currentVolume.name }, locale)}
              icon={<AlertTriangle className="w-5 h-5" />}
            />
            <Body>
              <div className="space-y-1.5">
                <Label htmlFor="delete-volume-name">
                  {interpolate(t.volume.typeToConfirm, { name: currentVolume.name }, locale)}
                </Label>
                <Input
                  id="delete-volume-name"
                  dir="ltr"
                  autoFocus
                  value={p.deleteVolumeInput}
                  onChange={(e) => p.setDeleteVolumeInput(e.target.value)}
                  placeholder={currentVolume.name}
                />
              </div>
            </Body>
            <Footer>
              <Button type="button" variant="outline" onClick={() => { p.setShowDeleteVolume(false); p.setDeleteVolumeInput(''); }}>
                {t.files.cancel}
              </Button>
              <Button
                type="button"
                variant="destructive"
                disabled={p.deleteVolumeInput.trim() !== currentVolume.name}
                onClick={() => {
                  p.onDeleteVolume(currentVolume.name);
                  p.setShowDeleteVolume(false);
                  p.setDeleteVolumeInput('');
                }}
              >
                {t.volume.deleteConfirm}
              </Button>
            </Footer>
          </div>
        </Modal>
      )}

      {p.onRebuildVolume && p.showRebuildConfirm && (
        <Modal onClose={() => p.setShowRebuildConfirm(false)} labelledBy="modal-rebuild-title" panelClassName="w-full max-w-md">
          <div className={PANEL_CLASS}>
            <Headline id="modal-rebuild-title" title={t.files.rebuildConfirmTitle} />
            <Body>
              <p className="text-xs text-zinc-600 dark:text-zinc-400 leading-relaxed">{t.files.rebuildConfirmDesc}</p>
            </Body>
            <Footer>
              <Button type="button" variant="outline" disabled={p.rebuilding} onClick={() => p.setShowRebuildConfirm(false)}>
                {t.files.cancel}
              </Button>
              <Button
                type="button"
                variant="destructive"
                disabled={p.rebuilding}
                onClick={async () => {
                  p.setRebuilding(true);
                  try {
                    await p.onRebuildVolume!(p.activeVolumeId);
                    p.setShowRebuildConfirm(false);
                  } catch (err) {
                    // 失败时保留弹窗，让用户看得见是哪一步出错
                    p.onNotifyError(errText(err));
                  } finally {
                    p.setRebuilding(false);
                  }
                }}
              >
                {t.files.rebuildConfirm}
              </Button>
            </Footer>
          </div>
        </Modal>
      )}
    </>
  );
};
