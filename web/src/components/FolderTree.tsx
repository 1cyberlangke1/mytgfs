import React, { useEffect, useMemo, useState } from 'react';
import { ChevronDown, ChevronRight, Folder, FolderOpen, Home } from 'lucide-react';
import { FileItem } from '../types';

// FolderTree 是资源管理器风格的目标目录选择器。
//
// 输入：dirs 当前卷所有目录；value 选中目录 id（null 表示根）；disabledIds 不可选中的目录
// 输出：点击行时回调 onChange(id | null)
// 预期：树形展开、自动展开选中项的祖先、被禁目录灰显不可点

interface FolderTreeProps {
  dirs: FileItem[];
  value: string | null;
  onChange: (id: string | null) => void;
  disabledIds?: Set<string>;
  rootLabel: string;
  ariaLabel: string;
}

interface Node {
  item: FileItem;
  children: Node[];
}

// buildTree 把扁平目录列表组装成树；父目录缺失时挂到根。
function buildTree(dirs: FileItem[]): Node[] {
  const byParent = new Map<string | null, FileItem[]>();
  const ids = new Set(dirs.map((d) => d.id));
  for (const d of dirs) {
    const parent = d.parentId && ids.has(d.parentId) ? d.parentId : null;
    const list = byParent.get(parent) ?? [];
    list.push(d);
    byParent.set(parent, list);
  }
  for (const list of byParent.values()) {
    list.sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: 'base' }));
  }
  const build = (parent: string | null): Node[] =>
    (byParent.get(parent) ?? []).map((item) => ({ item, children: build(item.id) }));
  return build(null);
}

// ancestorsOf 返回某目录的所有祖先 id，用于自动展开。
function ancestorsOf(dirs: FileItem[], id: string | null): Set<string> {
  const out = new Set<string>();
  if (!id) return out;
  const byId = new Map(dirs.map((d) => [d.id, d]));
  let cursor = byId.get(id)?.parentId ?? null;
  while (cursor) {
    out.add(cursor);
    cursor = byId.get(cursor)?.parentId ?? null;
  }
  return out;
}

export const FolderTree: React.FC<FolderTreeProps> = ({
  dirs,
  value,
  onChange,
  disabledIds,
  rootLabel,
  ariaLabel,
}) => {
  const tree = useMemo(() => buildTree(dirs), [dirs]);
  const [expanded, setExpanded] = useState<Set<string>>(() => ancestorsOf(dirs, value));

  // 选中项变化时展开它的祖先，否则树里看不到当前选择
  useEffect(() => {
    setExpanded((prev) => {
      const next = new Set(prev);
      for (const id of ancestorsOf(dirs, value)) next.add(id);
      return next;
    });
  }, [dirs, value]);

  const toggle = (id: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const row = (node: Node, depth: number) => {
    const { item, children } = node;
    const hasChildren = children.length > 0;
    const open = expanded.has(item.id);
    const disabled = disabledIds?.has(item.id) ?? false;
    const selected = value === item.id;
    return (
      <React.Fragment key={item.id}>
        <div
          role="treeitem"
          aria-selected={selected}
          aria-expanded={hasChildren ? open : undefined}
          aria-disabled={disabled}
          tabIndex={disabled ? -1 : 0}
          onClick={() => {
            if (!disabled) onChange(item.id);
          }}
          onKeyDown={(e) => {
            if (disabled) return;
            if (e.key === 'Enter' || e.key === ' ') {
              e.preventDefault();
              onChange(item.id);
            }
            if (e.key === 'ArrowRight' && hasChildren && !open) toggle(item.id);
            if (e.key === 'ArrowLeft' && hasChildren && open) toggle(item.id);
          }}
          className={`flex items-center gap-1.5 rounded-lg px-2 py-1.5 cursor-pointer text-xs select-none ${
            selected
              ? 'bg-[#007AFF]/12 text-[#007AFF] dark:text-[#0A84FF] font-medium'
              : disabled
                ? 'text-zinc-400 dark:text-zinc-600 cursor-not-allowed'
                : 'text-zinc-700 dark:text-zinc-300 hover:bg-zinc-100 dark:hover:bg-zinc-800/70'
          }`}
          style={{ paddingInlineStart: 8 + depth * 16 }}
        >
          {hasChildren ? (
            <button
              type="button"
              aria-label={open ? 'collapse' : 'expand'}
              onClick={(e) => {
                e.stopPropagation();
                toggle(item.id);
              }}
              className="p-0.5 rounded hover:bg-black/5 dark:hover:bg-white/10"
            >
              {open ? (
                <ChevronDown className="w-3 h-3" />
              ) : (
                <ChevronRight className="w-3 h-3" />
              )}
            </button>
          ) : (
            <span className="w-4 shrink-0" />
          )}
          {open ? (
            <FolderOpen className="w-3.5 h-3.5 shrink-0" />
          ) : (
            <Folder className="w-3.5 h-3.5 shrink-0" />
          )}
          <span dir="ltr" className="truncate">
            {item.name}
          </span>
        </div>
        {hasChildren && open && children.map((c) => row(c, depth + 1))}
      </React.Fragment>
    );
  };

  return (
    <div
      role="tree"
      aria-label={ariaLabel}
      className="max-h-64 overflow-y-auto rounded-xl border border-zinc-200/90 dark:border-zinc-800 p-1.5 space-y-0.5"
    >
      <div
        role="treeitem"
        aria-selected={value === null}
        tabIndex={0}
        onClick={() => onChange(null)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault();
            onChange(null);
          }
        }}
        className={`flex items-center gap-1.5 rounded-lg px-2 py-1.5 cursor-pointer text-xs select-none ${
          value === null
            ? 'bg-[#007AFF]/12 text-[#007AFF] dark:text-[#0A84FF] font-medium'
            : 'text-zinc-700 dark:text-zinc-300 hover:bg-zinc-100 dark:hover:bg-zinc-800/70'
        }`}
      >
        <span className="w-4 shrink-0" />
        <Home className="w-3.5 h-3.5 shrink-0" />
        <span className="truncate">{rootLabel}</span>
      </div>
      {tree.map((n) => row(n, 0))}
    </div>
  );
};
