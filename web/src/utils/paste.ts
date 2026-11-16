import type { UploadSource } from './uploadPlan';

/**
 * PasteContent 是一次粘贴能提供的内容。
 *
 * 输入：files 剪贴板里的文件；text 纯文本
 * 输出：无
 * 预期：两者互斥——有文件时不再带文字，避免「复制图片 + 文字」时同时开两个入口
 */
export interface PasteContent {
  files: UploadSource[];
  text: string;
}

/**
 * ClipboardSnapshot 是主动读剪贴板的结果。
 *
 * 输入：同 PasteContent，外加 blockedFileList
 * 输出：无
 * 预期：blockedFileList=true 表示剪贴板里是文件路径列表（从资源管理器复制的文件），
 *       浏览器只暴露 text/uri-list、不给本地文件字节，必须让用户改用 Ctrl+V
 */
export interface ClipboardSnapshot extends PasteContent {
  blockedFileList: boolean;
}

// 剪贴板里的图片常被浏览器命名为这些通用名，多张粘一起会互相覆盖，
// 所以这些名字一律改写成带时间戳的文件名；用户从资源管理器复制的真实文件名保持原样。
const GENERIC_CLIPBOARD_NAMES = new Set(['image.png', 'image.jpeg', 'image.jpg', 'blob']);

// 只承载标记语言、不承载文件内容的裁剪板类型：既不落成文件，也不当正文用
const MARKUP_TYPES = new Set(['text/html', 'text/rtf', 'text/uri-list']);

/**
 * isEditableTarget 判断焦点是否在可输入控件里。
 *
 * 输入：事件目标
 * 输出：true 表示应该放行原生粘贴
 * 预期：在设置页输入框、重命名框里粘贴时不能抢走用户的文字
 */
export function isEditableTarget(target: EventTarget | null): boolean {
  const el = target as (HTMLElement & { tagName?: string }) | null;
  if (!el || typeof el.tagName !== 'string') return false;
  const tag = el.tagName.toLowerCase();
  if (tag === 'input' || tag === 'textarea' || tag === 'select') return true;
  return el.isContentEditable === true;
}

// stamp 把毫秒时间戳格式化成 20261116-183012，用于生成不重名的粘贴文件名。
function stamp(ms: number): string {
  const d = new Date(ms);
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}${pad(d.getMonth() + 1)}${pad(d.getDate())}-${pad(d.getHours())}${pad(d.getMinutes())}${pad(d.getSeconds())}`;
}

// extFromMime 从 MIME 猜扩展名，认不出来就退回 'bin'。
function extFromMime(mime: string): string {
  const sub = (mime.split('/')[1] ?? '').split('+')[0]?.toLowerCase() ?? '';
  if (!sub) return 'bin';
  if (sub === 'jpeg') return 'jpg';
  if (sub === 'plain') return 'txt';
  // office 一类的长类型名不是可用扩展名
  return sub.length > 8 ? 'bin' : sub;
}

/**
 * pastedFileName 给剪贴板文件挑一个落盘文件名。
 *
 * 输入：file 剪贴板文件；now 时间戳（测试可注入）
 * 输出：保留真实文件名，或 `pasted-<时间戳>.<扩展名>`
 * 预期：真实文件名（用户复制文件）不被改写；浏览器的通用名改写成唯一名，
 *       否则同一目录连粘两张图会互相覆盖
 */
export function pastedFileName(file: File, now = Date.now()): string {
  const name = file.name.trim();
  if (name && !GENERIC_CLIPBOARD_NAMES.has(name.toLowerCase())) return name;
  const dot = name.lastIndexOf('.');
  const ext = dot > 0 ? name.slice(dot + 1) : extFromMime(file.type);
  return `pasted-${stamp(now)}.${ext || 'bin'}`;
}

// renamedFile 换掉 File 的名字：包一层 File，内容仍是指向原 Blob 的引用。
function renamedFile(file: File, name: string): File {
  if (file.name === name) return file;
  return new File([file], name, { type: file.type, lastModified: file.lastModified });
}

/**
 * uniqueNames 给同批粘贴里重名的文件加序号。
 *
 * 输入：来源列表
 * 输出：同长度列表，第 2 个同名文件起变成 `a-2.txt`、`a-3.txt`
 * 预期：一次复制多张同名截图也不会互相覆盖；列表顺序保持不变
 */
function uniqueNames(sources: UploadSource[]): UploadSource[] {
  const seen = new Map<string, number>();
  return sources.map((source) => {
    const name = source.file.name;
    const count = (seen.get(name) ?? 0) + 1;
    seen.set(name, count);
    if (count === 1) return source;
    const dot = name.lastIndexOf('.');
    const next =
      dot > 0 ? `${name.slice(0, dot)}-${count}${name.slice(dot)}` : `${name}-${count}`;
    return { file: renamedFile(source.file, next), rel: source.rel };
  });
}

/**
 * readClipboardContent 直接读系统剪贴板（不依赖焦点在页面上按 Ctrl+V）。
 *
 * 输入：now 时间戳（测试可注入）
 * 输出：{ files, text, blockedFileList }
 * 预期：
 *   - 图片与其它二进制类型（application/pdf、audio/*、video/* 等）都落成文件，
 *     扩展名按 MIME 推断，名字不重复
 *   - 只认 text/plain 作正文，富文本按纯文本处理
 *   - 从资源管理器复制文件时浏览器只给 text/uri-list：置 blockedFileList，
 *     由调用方提示改用 Ctrl+V（异步读取拿不到本地文件字节）
 *   - 浏览器不支持或用户拒绝授权时抛错
 */
export async function readClipboardContent(now = Date.now()): Promise<ClipboardSnapshot> {
  if (typeof navigator === 'undefined' || !navigator.clipboard?.read) {
    throw new Error('clipboard_unavailable');
  }
  const entries = await navigator.clipboard.read();
  const files: UploadSource[] = [];
  let text = '';
  let blockedFileList = false;
  for (const entry of entries) {
    if (entry.types.includes('text/uri-list')) {
      blockedFileList = true;
      continue;
    }
    const binaryType = entry.types.find(
      (type) => !type.startsWith('text/') && !MARKUP_TYPES.has(type)
    );
    if (binaryType) {
      const blob = await entry.getType(binaryType);
      files.push({
        file: new File([blob], `pasted-${stamp(now)}.${extFromMime(binaryType)}`, {
          type: binaryType,
        }),
        rel: '',
      });
      continue;
    }
    if (!text && entry.types.includes('text/plain')) {
      text = await (await entry.getType('text/plain')).text();
    }
  }
  return { files: uniqueNames(files), text, blockedFileList };
}

/**
 * extractPasteContent 从剪贴板事件取出可上传内容。
 *
 * 输入：clipboard 事件的 clipboardData；now 时间戳（测试可注入）
 * 输出：{ files, text }；两者都为空表示这次粘贴与上传无关
 * 预期：文件优先；只有纯文本时才返回 text，交给「新建文本」弹窗预填；
 *       支持任意多个任意类型的文件（Ctrl+V 是唯一能拿到本地文件字节的路径）
 */
export function extractPasteContent(clipboard: DataTransfer | null, now = Date.now()): PasteContent {
  const files: UploadSource[] = [];
  if (clipboard) {
    for (const item of Array.from(clipboard.items ?? [])) {
      if (item.kind !== 'file') continue;
      const file = item.getAsFile();
      if (!file) continue;
      files.push({ file: renamedFile(file, pastedFileName(file, now)), rel: '' });
    }
    // 个别浏览器 items 里取不到文件，但 files 列表是完整的
    if (files.length === 0) {
      for (const file of Array.from(clipboard.files ?? [])) {
        files.push({ file: renamedFile(file, pastedFileName(file, now)), rel: '' });
      }
    }
  }
  if (files.length > 0) return { files: uniqueNames(files), text: '' };
  const text = clipboard?.getData('text/plain') ?? '';
  return { files: [], text };
}
