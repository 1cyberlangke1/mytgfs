import type { UploadSource } from './uploadPlan';

/**
 * readAllEntries 读完一个目录的所有直接子项。
 *
 * 输入：dir 目录 entry
 * 输出：子项数组（文件与子目录混合）
 * 预期：readEntries 每次最多返回 100 项，返回空数组才代表读完；
 *       只读一次会把大目录后面的文件丢掉
 */
function readAllEntries(dir: FileSystemDirectoryEntry): Promise<FileSystemEntry[]> {
  return new Promise((resolve, reject) => {
    const reader = dir.createReader();
    const out: FileSystemEntry[] = [];
    const next = () => {
      reader.readEntries((batch) => {
        if (batch.length === 0) {
          resolve(out);
          return;
        }
        out.push(...batch);
        next();
      }, reject);
    };
    next();
  });
}

/**
 * readEntryFile 把文件 entry 取成 File。
 *
 * 输入：entry 文件 entry
 * 输出：File
 * 预期：读取失败时 reject，由外层把整次拖拽判为失败而不是静默少文件
 */
function readEntryFile(entry: FileSystemFileEntry): Promise<File> {
  return new Promise((resolve, reject) => entry.file(resolve, reject));
}

/**
 * walkEntry 递归展开拖进来的目录。
 *
 * 输入：entry 当前项；prefix 所在目录的相对路径（顶层为空串）；out 收集结果
 * 输出：无（写入 out）
 * 预期：目录内的文件带 `顶层目录/子目录/文件名` 相对路径，与
 *       input[webkitdirectory] 的 webkitRelativePath 语义一致；
 *       顶层单个文件 rel 为空串，沿用「直接落在当前目录」的旧行为
 */
async function walkEntry(
  entry: FileSystemEntry,
  prefix: string,
  out: UploadSource[]
): Promise<void> {
  if (entry.isDirectory) {
    const dirPrefix = `${prefix}${entry.name}/`;
    for (const child of await readAllEntries(entry as FileSystemDirectoryEntry)) {
      await walkEntry(child, dirPrefix, out);
    }
    return;
  }
  if (!entry.isFile) return;
  const file = await readEntryFile(entry as FileSystemFileEntry);
  out.push({ file, rel: prefix ? `${prefix}${file.name}` : '' });
}

/**
 * collectDroppedFiles 把一次拖放展开成上传来源列表。
 *
 * 输入：dt 拖放事件的 DataTransfer
 * 输出：UploadSource[]，目录已递归展开
 * 预期：
 *   - 优先走 webkitGetAsEntry，拖文件夹能拿到里面的文件
 *   - 浏览器不给 entry 时回落到 items/files 的平面文件列表
 *   - 完全不读内容，只建引用，几十 GB 的目录也不会在这里卡住
 */
export async function collectDroppedFiles(dt: DataTransfer): Promise<UploadSource[]> {
  const out: UploadSource[] = [];
  const items = Array.from(dt.items ?? []).filter((item) => item.kind === 'file');

  if (items.length > 0) {
    for (const item of items) {
      const entry = item.webkitGetAsEntry?.() ?? null;
      if (entry) {
        await walkEntry(entry, '', out);
        continue;
      }
      const file = item.getAsFile();
      if (file) out.push({ file, rel: '' });
    }
    return out;
  }

  for (const file of Array.from(dt.files ?? [])) out.push({ file, rel: '' });
  return out;
}
