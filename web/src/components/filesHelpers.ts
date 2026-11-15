// 文件页弹窗共用的名称/路径小工具。
//
// 输入：条目列表与用户输入
// 输出：校验错误文案或可读路径
// 预期：只做纯计算，不发请求，方便弹窗与页面各自复用
import type { FileItem } from '../types';
import type { Translations } from '../utils/i18n';
import { getItemFullPath } from '../utils/fsLogic';

// validateEntryName 校验重命名输入，返回错误文案或空串。
//
// 输入：name 输入值；item 原条目；targetId 目标目录；all 当前卷条目；t 文案
// 输出：可读错误；合法返回空串
// 预期：空名、含路径分隔符、目标目录重名都在提交前拦住
export function validateEntryName(
  name: string,
  item: FileItem,
  targetId: string | null,
  all: FileItem[],
  t: Translations
): string {
  const trimmed = name.trim();
  if (!trimmed) return t.files.nameRequired;
  if (/[\\/]/.test(trimmed)) return t.files.nameNoSeparator;
  if (trimmed === '.' || trimmed === '..') return t.files.nameNoSeparator;
  const clash = all.some(
    (f) =>
      f.volumeId === item.volumeId &&
      f.id !== item.id &&
      f.parentId === targetId &&
      f.name.toLowerCase() === trimmed.toLowerCase()
  );
  return clash ? t.files.nameExists : '';
}

// joinPath 拼接目录与文件名，根目录不产生双斜杠。
export function joinPath(dir: string, name: string): string {
  if (dir === '/' || dir === '') return `/${name}`;
  return `${dir}/${name}`;
}

// targetPathText 把目标目录转成可读路径，用于弹窗预览。
export function targetPathText(all: FileItem[], volumeId: string, targetId: string | null): string {
  if (!targetId) return '/';
  const dir = all.find((f) => f.id === targetId && f.volumeId === volumeId);
  return dir ? getItemFullPath(all, dir) : '/';
}
