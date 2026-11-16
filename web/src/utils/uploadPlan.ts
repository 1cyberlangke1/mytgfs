import type { TransferTask } from '../types';
import { inferFileCategory } from './fsLogic';

/**
 * UploadSource 是一次上传来源：文件本体 + 卷内相对路径。
 *
 * 输入：file 浏览器 File；rel 相对路径
 * 输出：无
 * 预期：rel 为空表示直接落在目标目录；非空必须带文件名（目录上传由
 *       webkitRelativePath / 拖拽展开提供），与后端 uploadTarget 的约定一致
 */
export interface UploadSource {
  file: File;
  rel: string;
}

/**
 * StagedUpload 是「已选好、还没开始传」的草稿项。
 *
 * 输入：UploadSource + 目标卷/目录 + 稳定 id
 * 输出：无
 * 预期：确认前不进 uploadQueue、不发 /api/upload/init；
 *       移除即从草稿里消失，服务端感知不到任何痕迹
 */
export interface StagedUpload extends UploadSource {
  id: string;
  volumeId: string;
  parentId: string | null;
}

let stagedSeq = 0;
let taskSeq = 0;

/**
 * newStagedID 生成草稿项 id。
 *
 * 输入：now 时间戳（测试可注入）
 * 输出：`staged-<时间戳>-<自增序号>` 形式的唯一 id
 * 预期：同一毫秒内连续添加也不冲突
 */
export function newStagedID(now = Date.now()): string {
  stagedSeq += 1;
  return `staged-${now}-${stagedSeq}`;
}

/**
 * newTaskID 生成上传任务 id。
 *
 * 输入：now 时间戳（测试可注入）
 * 输出：`task-<时间戳>-<自增序号>`
 * 预期：与 TransferTask 现有 id 体例保持一致（task- 前缀）
 */
export function newTaskID(now = Date.now()): string {
  taskSeq += 1;
  return `task-${now}-${taskSeq}`;
}

/**
 * sourcesFromFiles 把 input[file] / input[webkitdirectory] 的选择结果转成来源列表。
 *
 * 输入：FileList 或 File 数组
 * 输出：UploadSource[]；普通选择时 rel 为空串
 * 预期：目录选择时保留 webkitRelativePath 的层级（含顶层目录名）
 */
export function sourcesFromFiles(files: ArrayLike<File>): UploadSource[] {
  const out: UploadSource[] = [];
  for (const file of Array.from(files)) {
    const rel = (file as File & { webkitRelativePath?: string }).webkitRelativePath ?? '';
    out.push({ file, rel });
  }
  return out;
}

/**
 * stageSources 把来源列表转成草稿项。
 *
 * 输入：sources 来源；volumeId 目标卷；parentId 目标目录（null 为根）
 * 输出：StagedUpload[]，每项带唯一 id
 * 预期：只做数据转换，不触发网络请求
 */
export function stageSources(
  sources: readonly UploadSource[],
  volumeId: string,
  parentId: string | null
): StagedUpload[] {
  return sources.map((source) => ({
    id: newStagedID(),
    file: source.file,
    rel: source.rel,
    volumeId,
    parentId,
  }));
}

/**
 * stagedTotalBytes 汇总草稿总字节数。
 *
 * 输入：草稿项列表
 * 输出：字节数之和
 * 预期：空列表返回 0
 */
export function stagedTotalBytes(items: readonly StagedUpload[]): number {
  return items.reduce((sum, item) => sum + item.file.size, 0);
}

/**
 * stagedDisplayPath 取用于展示的卷内路径。
 *
 * 输入：草稿项
 * 输出：有相对路径时用相对路径，否则用文件名
 * 预期：列表里能看出文件在哪个子目录，而不是只剩同名文件
 */
export function stagedDisplayPath(item: StagedUpload): string {
  return item.rel || item.file.name;
}

/**
 * keptStaged 按「被移除的 id 集合」筛出仍要上传的草稿项。
 *
 * 输入：items 全部草稿；hidden 被移除的 id
 * 输出：保持原顺序的剩余项
 * 预期：全不选时返回空数组，由调用方禁用「开始上传」
 */
export function keptStaged(
  items: readonly StagedUpload[],
  hidden: ReadonlySet<string>
): StagedUpload[] {
  return items.filter((item) => !hidden.has(item.id));
}

/**
 * toTransferTasks 把草稿项转成上传任务。
 *
 * 输入：items 待传草稿；batchId 批次号
 * 输出：TransferTask[]，state 一律 queued，进度归零
 * 预期：字段与旧 handleUploadFiles 内联构造保持一致，概览页的整批进度
 *       依旧按 batchId 汇总；不写 uploadInputs（那是调用方的事）
 */
export function toTransferTasks(
  items: readonly StagedUpload[],
  batchId: string
): TransferTask[] {
  return items.map((item) => ({
    id: newTaskID(),
    fileName: item.file.name,
    volumeId: item.volumeId,
    targetParentId: item.parentId,
    direction: 'upload',
    size: item.file.size,
    transferredBytes: 0,
    state: 'queued',
    speedBps: 0,
    isChunkedResume: false,
    chunkSize: 0,
    currentChunk: 0,
    totalChunks: 1,
    createdAt: new Date().toISOString(),
    category: inferFileCategory(item.file.name, false),
    opKind: 'upload',
    batchId,
  }));
}
