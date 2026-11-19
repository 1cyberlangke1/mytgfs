import { useEffect, useRef, useState, type MutableRefObject } from 'react';
import type { FileItem, Locale, NavTab, TransferTask } from '../types';
import type { Translations } from '../utils/i18n';
import { interpolate } from '../utils/i18n';
import {
  UploadPaused,
  cancelQueueJob,
  clearQueue,
  discardUpload,
  importFileNative,
  importFolderNative,
  uploadFile,
  uploadPoster,
  type UploadResume,
} from '../api';
import { validateAndPlanUpload } from '../utils/fsLogic';
import { makeVideoPoster } from '../utils/poster';
import { errText } from '../utils/errorText';
import { platformCaps } from '../platform';
import { extractPasteContent, isEditableTarget, readClipboardContent } from '../utils/paste';
import { sourcesFromFiles, stageSources, toTransferTasks, uploadArgs, type StagedUpload, type UploadSource } from '../utils/uploadPlan';
import { buildDirIndex, detectConflicts, resolveUploadConflicts, type ConflictAction, type UploadConflict } from '../utils/uploadConflict';

// MAX_PARALLEL_UPLOADS 是客户端同时在传的文件数：会话有空闲回收，3 路既跑满带宽也留得住会话。
const MAX_PARALLEL_UPLOADS = 3;

type Input = {
  file: File;
  volumeId: string;
  parentId: string | null;
  rel: string;
  name: string;
  task: TransferTask;
  resume?: UploadResume;
  sessionId?: string;
};

export function useUploads(p: {
  t: Translations;
  locale: Locale;
  tRef: MutableRefObject<Translations>;
  localeRef: MutableRefObject<Locale>;
  activeTab: NavTab;
  activeVolumeId: string;
  currentFolderId: string | null;
  files: FileItem[];
  pushToast: (text: string, type?: 'success' | 'error' | 'pending') => string;
  setServerQueue: (updater: (prev: TransferTask[]) => TransferTask[]) => void;
  onConnectedRef: MutableRefObject<() => void>;
  reloadFiles: (names?: string[]) => Promise<void>;
  reloadState: () => Promise<string[] | null>;
}) {
  const { t, locale, tRef, localeRef, activeTab, activeVolumeId, currentFolderId, files } = p;
  const { pushToast, setServerQueue, onConnectedRef, reloadFiles, reloadState } = p;
  const [uploadTasks, setUploadTasks] = useState<TransferTask[]>([]);
  // stagedUploads 是「已选好、还没开始传」的清单：确认前不发任何请求
  const [stagedUploads, setStagedUploads] = useState<StagedUpload[]>([]);
  const [pendingConflicts, setPendingConflicts] = useState<{ items: StagedUpload[]; conflicts: UploadConflict[] } | null>(null);
  const [textDraft, setTextDraft] = useState<{ initial: string } | null>(null);

  const uploadAbort = useRef(new Map<string, AbortController>());
  const uploadInputs = useRef(new Map<string, Input>());
  // 客户端并发闸门：一次性全发出去后面的文件会撞 404「会话过期」
  const uploadQueue = useRef<string[]>([]);
  const activeUploads = useRef(0);
  // 速度采样：只展示真实测得的字节增量，不编造速率
  const uploadSpeed = useRef(new Map<string, { bytes: number; at: number }>());

  const patchTask = (id: string, patch: Partial<TransferTask>) =>
    setUploadTasks((prev) => prev.map((tr) => (tr.id === id ? { ...tr, ...patch } : tr)));

  // pumpUploads 按并发上限启动排队中的上传；名额释放后自动续跑下一个
  const pumpUploads = () => {
    while (activeUploads.current < MAX_PARALLEL_UPLOADS) {
      const id = uploadQueue.current.shift();
      if (!id) return;
      const input = uploadInputs.current.get(id);
      if (!input) continue;
      activeUploads.current += 1;
      void runUpload(input);
    }
  };

  // 单文件上传：更新任务进度，成功后刷新索引；断点保存在 uploadInputs 里
  const runUpload = async (input: Input) => {
    const { task, file, rel, name } = input;
    const ctl = new AbortController();
    uploadAbort.current.set(task.id, ctl);
    patchTask(task.id, { state: 'transferring', phase: 'local' });
    try {
      const res = await uploadFile(
        input.volumeId,
        input.parentId || '/',
        rel,
        name,
        file,
        (ratio) => {
          const bytes = Math.round(task.size * ratio);
          const now = performance.now();
          const prev = uploadSpeed.current.get(task.id);
          const bps = prev && now > prev.at ? Math.max(0, ((bytes - prev.bytes) * 1000) / (now - prev.at)) : 0;
          uploadSpeed.current.set(task.id, { bytes, at: now });
          setUploadTasks((prevTasks) =>
            prevTasks.map((tr) =>
              tr.id === task.id
                ? { ...tr, transferredBytes: bytes, speedBps: bps > 0 ? bps : tr.speedBps }
                : tr
            )
          );
        },
        ctl.signal,
        input.resume,
        (id) => {
          // 记住分片会话：取消时据此丢弃服务端临时文件
          const cur = uploadInputs.current.get(task.id);
          if (cur) cur.sessionId = id;
        }
      );
      // commit 只等到任务落库：Telegram 上传在队列里继续，字节进度由队列快照接管
      patchTask(task.id, {
        state: res.job > 0 ? 'queued' : 'completed',
        phase: res.job > 0 ? 'remote' : 'local',
        queueJobId: res.job > 0 ? res.job : undefined,
        transferredBytes: res.job > 0 ? 0 : task.size,
        speedBps: 0,
      });
      // 视频封面放在「完成」之后：生成封面要解码，放前面会让界面停在 100% 却仍显示上传中
      if (file.type.startsWith('video/')) {
        try {
          const poster = await makeVideoPoster(file);
          if (poster) await uploadPoster(input.volumeId, res.path, poster);
        } catch {
          // 浏览器不支持解码该视频时保持 Telegram 缩略图或占位图
        }
      }
      uploadAbort.current.delete(task.id);
      // 传完就不再需要 File 与速率采样：留着会把整份文件钉在内存里
      uploadInputs.current.delete(task.id);
      uploadSpeed.current.delete(task.id);
      await reloadFiles([input.volumeId]);
      await reloadState();
    } catch (err) {
      // 暂停时保留服务端会话与已传字节，继续时从断点续传
      const paused = err instanceof UploadPaused;
      const stopped = paused || (err instanceof Error && err.message === 'aborted');
      if (paused) {
        const cur = uploadInputs.current.get(task.id);
        if (cur) cur.resume = { id: err.id, offset: err.offset };
        // 取消上传会先删掉 uploadInputs 再 abort，这里据此丢弃服务端临时文件
        else void discardUpload(err.id);
      }
      patchTask(task.id, {
        state: stopped ? 'paused' : 'failed',
        speedBps: 0,
        errorMessage: stopped ? undefined : errText(err),
      });
      uploadAbort.current.delete(task.id);
      if (!stopped) pushToast(errText(err), 'error');
    } finally {
      // 无论成功、失败还是暂停，都要让出并发名额
      activeUploads.current = Math.max(0, activeUploads.current - 1);
      pumpUploads();
    }
  };

  // 上传前预检：单个文件不合格只跳过它并在提示里点名，不整批丢弃
  const handleUploadSources = (sources: UploadSource[], volumeId: string, parentId: string | null) => {
    const accepted = sources.filter(({ file }) => {
      const verdict = validateAndPlanUpload(file.name, file.size);
      if (verdict.valid) return true;
      pushToast(
        verdict.errorCode === 'FILE_TOO_LARGE'
          ? interpolate(tRef.current.files.uploadTooLarge, { name: file.name }, localeRef.current)
          : tRef.current.files.uploadEmptyName,
        'error'
      );
      return false;
    });
    if (accepted.length === 0) return;
    // 清单已打开时继续追加（再拖一批/再粘一张），不重开弹窗
    setStagedUploads((prev) => [...prev, ...stageSources(accepted, volumeId, parentId)]);
  };

  // 目录选择时把 webkitRelativePath 原样带进清单
  const handleUploadFiles = (fileList: FileList, volumeId: string, parentId: string | null) =>
    handleUploadSources(sourcesFromFiles(fileList), volumeId, parentId);

  // startUpload 把最终清单转成上传任务并入队；改名写进 init 的 rel/name，不额外发重命名请求
  const startUpload = (items: StagedUpload[]) => {
    setStagedUploads([]);
    if (items.length === 0) return;
    // 同一次确认共享批次号，概览页据此显示整批总进度
    const batchId = `batch-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;
    const plan = toTransferTasks(items, batchId);
    items.forEach((item, index) => {
      const task = plan[index]!;
      uploadInputs.current.set(task.id, { file: item.file, volumeId: item.volumeId, parentId: item.parentId, ...uploadArgs(item), task });
    });
    // 最新的任务排在最上面，与操作队列显示顺序一致
    setUploadTasks((prev) => [...plan, ...prev]);
    pushToast(interpolate(t.files.uploadStartedToast, { name: plan[0]?.fileName ?? '' }, locale), 'success');
    uploadQueue.current.push(...plan.map((task) => task.id));
    pumpUploads();
  };

  // 安卓原生导入：字节按 fd 直读，不进 JS；取消（0,0）不提示
  const runNativeImport = async (
    run: () => Promise<{ uploaded: number; failed: number; failedNames?: string[] }>,
    volumeId: string,
    successKey: string,
    failKey: string
  ) => {
    try {
      const res = await run();
      if (res.uploaded > 0) {
        pushToast(interpolate(successKey, { n: String(res.uploaded) }, locale), 'success');
        void reloadFiles([volumeId]);
      }
      if (res.failed > 0) {
        pushToast(interpolate(failKey, { n: String(res.failed) }, locale), 'error');
        const names = res.failedNames ?? [];
        if (names.length > 0) {
          pushToast(interpolate(t.files.importFailedNames, { names: names.join(locale === 'zh-CN' ? '、' : ', ') }, locale), 'error');
        }
      }
    } catch (err) {
      pushToast(errText(err), 'error');
    }
  };
  const handleImportFolder = (volumeId: string, dir: string) =>
    runNativeImport(() => importFolderNative(volumeId, dir), volumeId, t.files.folderImportedToast, t.files.folderImportFailed);
  const handleImportFiles = (volumeId: string, dir: string) =>
    runNativeImport(() => importFileNative(volumeId, dir), volumeId, t.files.folderImportedToast, t.files.folderImportFailed);

  // 确认清单：先查同名，没有冲突直接传；有冲突先收起清单再问处理方式
  const handleConfirmUpload = (kept: StagedUpload[]) => {
    if (kept.length === 0) {
      setStagedUploads([]);
      return;
    }
    const index = buildDirIndex(files);
    const conflicts = detectConflicts(
      kept.map((item) => ({ id: item.id, name: item.name, dirKey: index.keyOf(item) })),
      index.namesByDir
    );
    if (conflicts.length === 0) return startUpload(kept);
    setStagedUploads([]);
    setPendingConflicts({ items: kept, conflicts });
  };

  // 覆盖保留原名（后端按同路径覆盖），改名换建议名，跳过整项丢弃
  const handleResolveConflicts = (choices: Record<string, ConflictAction>) => {
    const pending = pendingConflicts;
    if (!pending) return;
    setPendingConflicts(null);
    startUpload(resolveUploadConflicts(pending.items, pending.conflicts, choices));
  };

  // 关掉冲突弹窗：把清单放回来继续改，不丢已选文件
  const handleCancelConflicts = () => {
    const pending = pendingConflicts;
    setPendingConflicts(null);
    if (pending) setStagedUploads(pending.items);
  };
  const handleCancelUploadPlan = () => setStagedUploads([]);

  // 新建文本包成普通 File，走同一条上传清单
  const handleTextFileSave = (name: string, text: string) => {
    setTextDraft(null);
    handleUploadSources([{ file: new File([text], name, { type: 'text/plain;charset=utf-8' }), rel: '' }], activeVolumeId, currentFolderId);
  };

  // 直接读系统剪贴板：图片/文件进清单，纯文本打开新建文本并预填
  const handleClipboardUpload = async () => {
    try {
      const { files: pasted, text, blockedFileList } = await readClipboardContent();
      if (pasted.length > 0) return handleUploadSources(pasted, activeVolumeId, currentFolderId);
      if (text.trim().length > 0) return setTextDraft({ initial: text });
      pushToast(blockedFileList ? tRef.current.files.clipboardFileList : tRef.current.files.clipboardEmpty, 'error');
    } catch {
      pushToast(tRef.current.files.clipboardDenied, 'error');
    }
  };

  // 文件页粘贴入口：焦点在输入控件里时放行原生粘贴
  useEffect(() => {
    if (!platformCaps.paste || activeTab !== 'files') return;
    const onPaste = (e: ClipboardEvent) => {
      if (isEditableTarget(e.target)) return;
      const { files: pasted, text } = extractPasteContent(e.clipboardData);
      if (pasted.length === 0 && !text) return;
      e.preventDefault();
      if (pasted.length > 0) return handleUploadSources(pasted, activeVolumeId, currentFolderId);
      setTextDraft({ initial: text });
    };
    document.addEventListener('paste', onPaste);
    return () => document.removeEventListener('paste', onPaste);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeTab, activeVolumeId, currentFolderId]);

  const handleToggleTransferPause = (taskId: string) => {
    const task = uploadTasks.find((tr) => tr.id === taskId);
    if (!task) return;
    if (task.state === 'transferring') return void uploadAbort.current.get(taskId)?.abort();
    if (task.state === 'queued') {
      // 还没开始的任务直接出队，不占并发名额
      uploadQueue.current = uploadQueue.current.filter((id) => id !== taskId);
      return patchTask(taskId, { state: 'paused' });
    }
    if (task.state === 'paused') {
      if (!uploadInputs.current.get(taskId)) return;
      patchTask(taskId, { state: 'queued' });
      uploadQueue.current.push(taskId);
      pumpUploads();
    }
  };

  // 重试失败的上传：重新排队，不沿用可能已失效的会话
  const handleRetryTransfer = (taskId: string) => {
    const input = uploadInputs.current.get(taskId);
    if (!input) return;
    uploadInputs.current.set(taskId, { ...input, resume: undefined });
    patchTask(taskId, { state: 'queued', errorMessage: undefined, speedBps: 0 });
    uploadQueue.current.push(taskId);
    pumpUploads();
  };

  const handleCancelTransfer = (taskId: string) => {
    uploadAbort.current.get(taskId)?.abort();
    uploadAbort.current.delete(taskId);
    // 会话已建立的要让服务端丢掉分片会话与临时字节
    const input = uploadInputs.current.get(taskId);
    if (input?.sessionId) void discardUpload(input.sessionId);
    uploadInputs.current.delete(taskId);
    uploadQueue.current = uploadQueue.current.filter((id) => id !== taskId);
    setUploadTasks((prev) => prev.filter((tr) => tr.id !== taskId));
  };

  // 取消服务端队列任务：先本地落「已取消」让界面立刻响应，失败再拉快照回滚
  const handleCancelQueueJob = (task: TransferTask) => {
    if (!task.queueVolume || task.queueJobId === undefined) return;
    setServerQueue((prev) => prev.map((tr) => (tr.id === task.id ? { ...tr, state: 'cancelled', speedBps: 0 } : tr)));
    cancelQueueJob(task.queueVolume, task.queueJobId)
      .then(() => pushToast(t.overview.queueCancelledToast, 'success'))
      .catch((err) => {
        pushToast(errText(err), 'error');
        onConnectedRef.current();
      });
  };

  // 服务端持久化表才是队列的真实来源：只清本地数组等于假按钮
  const handleClearCompletedTransfers = () => {
    clearQueue()
      .then(() => {
        for (const tr of uploadTasks.filter((x) => x.state === 'completed' || x.state === 'failed')) {
          uploadInputs.current.delete(tr.id);
          uploadSpeed.current.delete(tr.id);
        }
        setUploadTasks((prev) => prev.filter((tr) => tr.state !== 'completed' && tr.state !== 'failed'));
        pushToast(t.overview.queueClearedToast, 'success');
      })
      .catch((err) => pushToast(errText(err), 'error'));
  };

  return {
    uploadTasks,
    stagedUploads,
    pendingConflicts,
    textDraft,
    setTextDraft,
    handleUploadSources,
    handleUploadFiles,
    handleImportFolder,
    handleImportFiles,
    handleConfirmUpload,
    handleResolveConflicts,
    handleCancelConflicts,
    handleCancelUploadPlan,
    handleTextFileSave,
    handleClipboardUpload,
    handleToggleTransferPause,
    handleRetryTransfer,
    handleCancelTransfer,
    handleCancelQueueJob,
    handleClearCompletedTransfers,
  };
}
