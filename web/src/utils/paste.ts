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

// 剪贴板里的图片常被浏览器命名为这些通用名，多张粘一起会互相覆盖，
// 所以这些名字一律改写成带时间戳的文件名；用户从资源管理器复制的真实文件名保持原样。
const GENERIC_CLIPBOARD_NAMES = new Set(['image.png', 'image.jpeg', 'image.jpg', 'blob']);

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
  return sub;
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
 * extractPasteContent 从剪贴板事件取出可上传内容。
 *
 * 输入：clipboard 事件的 clipboardData；now 时间戳（测试可注入）
 * 输出：{ files, text }；两者都为空表示这次粘贴与上传无关
 * 预期：文件优先；只有纯文本时才返回 text，交给「新建文本」弹窗预填
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
  }
  if (files.length > 0) return { files, text: '' };
  const text = clipboard?.getData('text/plain') ?? '';
  return { files: [], text };
}
