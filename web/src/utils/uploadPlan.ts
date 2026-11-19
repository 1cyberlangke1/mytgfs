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
 * 输入：UploadSource + 目标卷/目录 + 稳定 id + 生效文件名
 * 输出：无
 * 预期：确认前不进 uploadQueue、不发 /api/upload/init；
 *       移除即从草稿里消失，服务端感知不到任何痕迹
 */
export interface StagedUpload extends UploadSource {
  id: string;
  volumeId: string;
  parentId: string | null;
  /**
   * name 是真正落盘用的文件名，默认取 file.name，可在清单里改。
   *
   * 改名发生在 /api/upload/init 之前，因此不额外发一次重命名请求；
   * 文件字节与 File 对象原样保留，改名不动内容也不复制内容。
   */
  name: string;
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
function newStagedID(now = Date.now()): string {
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
function newTaskID(now = Date.now()): string {
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
    name: source.file.name,
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
 * relDirPrefix 取相对路径里的目录部分。
 *
 * 输入：rel，可能为空、可能带反斜杠
 * 输出：以 "/" 结尾的目录前缀；rel 为空或没有目录成分时返回 ""
 * 预期：改名只替换最后一段，目录层级原样保留
 */
export function relDirPrefix(rel: string): string {
  const normalized = rel.replaceAll('\\', '/');
  const cut = normalized.lastIndexOf('/');
  return cut >= 0 ? normalized.slice(0, cut + 1) : '';
}

/**
 * stagedDisplayPath 取用于展示的卷内路径。
 *
 * 输入：草稿项
 * 输出：目录前缀 + 生效文件名；没有相对路径时只剩文件名
 * 预期：清单里改过名的项按新名字显示，也能看出它落在哪个子目录
 */
export function stagedDisplayPath(item: StagedUpload): string {
  return `${relDirPrefix(item.rel)}${item.name}`;
}

/**
 * UploadNameError 是前端改名校验的失败原因。
 *
 * 输入：无
 * 输出：'empty' 空名或 . / ..；'separator' 含路径分隔符或 NUL；
 *       'too_long' 超过后端 255 字节上限
 * 预期：与 internal/dirent 的 Node.validate 保持一致，提前拦下而不是等后端 400
 */
export type UploadNameError = 'empty' | 'separator' | 'too_long';

// 与 internal/dirent 的 MaxNameBytes 对齐：后端按 UTF-8 字节算长度
const UPLOAD_NAME_MAX_BYTES = 255;

/**
 * validateUploadName 校验用户改过的文件名。
 *
 * 输入：raw 用户输入的原始名字
 * 输出：合法返回 null；否则返回 UploadNameError
 * 预期：名字两端空白会被后续 withName 去掉，因此这里先 trim 再判断
 */
export function validateUploadName(raw: string): UploadNameError | null {
  const name = raw.trim();
  if (name === '' || name === '.' || name === '..') return 'empty';
  if (/[/\\\u0000]/.test(name)) return 'separator';
  if (new TextEncoder().encode(name).length > UPLOAD_NAME_MAX_BYTES) return 'too_long';
  return null;
}

/**
 * withName 把草稿项换成新文件名。
 *
 * 输入：item 原草稿；raw 用户输入的名字
 * 输出：新草稿项，name 已 trim，其余字段不变
 * 预期：file / rel / id 都不动，改名只影响落盘时的最后一段
 */
export function withName(item: StagedUpload, raw: string): StagedUpload {
  return { ...item, name: raw.trim() };
}

/**
 * UploadTarget 是 /api/upload/init 需要的落点参数。
 *
 * 输入：无
 * 输出：rel 与 name 两个字段
 * 预期：见 uploadArgs
 */
export interface UploadTarget {
  rel: string;
  name: string;
}

/**
 * uploadArgs 算出上传接口真正该收的 rel 与 name。
 *
 * 输入：草稿项
 * 输出：rel 带目录时把最后一段换成生效文件名，否则 rel 留空；
 *       name 一律是生效文件名
 * 预期：后端 uploadTarget 在 rel 非空时只认 rel、忽略 name，
 *       所以目录内改过名的文件必须把新名字写进 rel，否则会按原名落盘
 */
export function uploadArgs(item: StagedUpload): UploadTarget {
  const prefix = relDirPrefix(item.rel);
  return { rel: prefix ? `${prefix}${item.name}` : '', name: item.name };
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
    fileName: item.name,
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
    category: inferFileCategory(item.name, false),
    opKind: 'upload',
    batchId,
  }));
}
