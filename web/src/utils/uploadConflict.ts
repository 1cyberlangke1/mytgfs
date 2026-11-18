import { relDirPrefix, type StagedUpload } from './uploadPlan';

/**
 * 同名冲突检测与消解（纯函数，无网络）。
 *
 * 输入：待上传清单 + 面板已有的目录树
 * 输出：冲突列表（含建议新名）、按选择处理后的清单
 * 预期：
 *   - 比较的是「卷内真实目标路径」，目录上传 `dir/a.txt` 不会跟根目录的 `a.txt` 打架
 *   - 同一批里重复挑同一个落点也算冲突，不能只比对服务端已有文件
 *   - 建议名插在扩展名前：`a.txt` → `a (2).txt`，且跳过已占用的编号
 *   - 提交前只做一次判断，覆盖/改名/跳过由用户逐项决定
 */

/** 同目录同名的处理方式。 */
export type ConflictAction = 'overwrite' | 'rename' | 'skip';

/** 一条冲突：原名与建议的新名字。 */
export interface UploadConflict {
  /** 对应 StagedUpload.id */
  id: string;
  /** 用户当前填的名字 */
  name: string;
  /** 建议的新名字（保证不与同目录的已知名字重复） */
  suggested: string;
}

/**
 * TreeEntry 是构建目录索引所需的最小字段。
 *
 * 输入：面板返回的文件条目（FileItem 满足这个形状）
 * 输出：无
 * 预期：只依赖 id/parentId 串出完整路径，不关心大小、类型等其它字段
 */
export interface TreeEntry {
  id: string;
  volumeId: string;
  parentId: string | null;
  name: string;
}

/**
 * dirKey 把「卷 + 卷内目录路径」压成一个比较键。
 *
 * 输入：volumeId；dirPath 卷内目录路径，根目录传空串
 * 输出：带分隔符的字符串
 * 预期：不同卷相同的目录名不会混为一谈
 */
export function dirKey(volumeId: string, dirPath: string): string {
  return `${volumeId}\u0000${dirPath}`;
}

/**
 * joinDir 把父目录路径与相对目录前缀拼起来。
 *
 * 输入：base 父目录（空串为根）；rel 相对前缀，可能带反斜杠与首尾斜杠
 * 输出：不带首尾斜杠的路径
 * 预期：rel 为空时原样返回 base，统一用 "/" 分隔
 */
function joinDir(base: string, rel: string): string {
  const clean = rel.replaceAll('\\', '/').replace(/^\/+|\/+$/g, '');
  if (!clean) return base;
  return base ? `${base}/${clean}` : clean;
}

/** 目录索引：每个目录已占用的名字，以及草稿项该落到哪个目录。 */
export interface DirIndex {
  /** 键为 dirKey，值为该目录下的条目名 */
  namesByDir: Record<string, string[]>;
  /**
   * keyOf 算出草稿项的目标目录键。
   *
   * 输入：草稿项的卷、父目录 id 与相对路径
   * 输出：dirKey(volumeId, parentPath + rel 目录前缀)
   * 预期：父目录不在索引里（已删/刚建）时按卷根处理，与后端容错一致
   */
  keyOf: (item: Pick<StagedUpload, 'volumeId' | 'parentId' | 'rel'>) => string;
}

/**
 * buildDirIndex 把面板的扁平文件列表整理成「按目录分组」的索引。
 *
 * 输入：entries 全量条目（id/parentId 构树）
 * 输出：目录名集合 + 草稿项落点计算函数
 * 预期：父链上溯带环检测与结果缓存，重复目录只算一次；
 *       父级缺失或成环时回落到卷根，不抛异常
 */
export function buildDirIndex(entries: readonly TreeEntry[]): DirIndex {
  const byKey = new Map<string, TreeEntry>();
  for (const entry of entries) byKey.set(`${entry.volumeId}\u0000${entry.id}`, entry);

  // 缓存每个条目的「自身完整路径」；目录就是它自己的路径
  const pathCache = new Map<string, string>();

  const resolvePath = (volumeId: string, id: string): string => {
    const cacheKey = `${volumeId}\u0000${id}`;
    const cached = pathCache.get(cacheKey);
    if (cached !== undefined) return cached;

    // 自顶向下还原：先收集未命中缓存的祖先链，再反向拼路径
    const chain: TreeEntry[] = [];
    const seen = new Set<string>();
    let cursor = byKey.get(cacheKey);
    let base = '';
    while (cursor) {
      const key = `${volumeId}\u0000${cursor.id}`;
      const hit = pathCache.get(key);
      if (hit !== undefined) {
        base = hit;
        break;
      }
      if (seen.has(key)) {
        // 成环：整段按卷根处理，避免死循环
        base = '';
        chain.length = 0;
        break;
      }
      seen.add(key);
      chain.push(cursor);
      cursor = cursor.parentId ? byKey.get(`${volumeId}\u0000${cursor.parentId}`) : undefined;
    }

    let acc = base;
    for (let i = chain.length - 1; i >= 0; i--) {
      const node = chain[i]!;
      acc = acc ? `${acc}/${node.name}` : node.name;
      pathCache.set(`${volumeId}\u0000${node.id}`, acc);
    }
    return pathCache.get(cacheKey) ?? '';
  };

  const namesByDir: Record<string, string[]> = {};
  for (const entry of entries) {
    const dir = entry.parentId ? resolvePath(entry.volumeId, entry.parentId) : '';
    const key = dirKey(entry.volumeId, dir);
    (namesByDir[key] ??= []).push(entry.name);
  }

  const keyOf = (item: Pick<StagedUpload, 'volumeId' | 'parentId' | 'rel'>): string => {
    const parentPath = item.parentId ? resolvePath(item.volumeId, item.parentId) : '';
    const targetDir = joinDir(parentPath, relDirPrefix(item.rel));
    return dirKey(item.volumeId, targetDir);
  };

  return { namesByDir, keyOf };
}

/**
 * uniqueName 生成不与 taken 冲突的名字。
 *
 * 输入：name 原文件名；taken 同目录已占用名字集合（区分大小写，与 Telegram 一致）
 * 输出：可用名字：`base (2).ext`、`base (3).ext`……
 * 预期：没有扩展名（或以点开头）时整体当基名；最多试 9999 次后回落到时间戳
 */
export function uniqueName(name: string, taken: ReadonlySet<string>): string {
  if (!taken.has(name)) return name;
  const dot = name.lastIndexOf('.');
  const hasExt = dot > 0 && dot < name.length - 1;
  const base = hasExt ? name.slice(0, dot) : name;
  const ext = hasExt ? name.slice(dot) : '';
  for (let n = 2; n <= 9999; n++) {
    const candidate = `${base} (${n})${ext}`;
    if (!taken.has(candidate)) return candidate;
  }
  return `${base} (${Date.now()})${ext}`;
}

/** 冲突检测的输入行：草稿 id、生效名字、目标目录键。 */
export interface ConflictRow {
  id: string;
  name: string;
  dirKey: string;
}

/**
 * detectConflicts 找出与目标目录里已有条目或同批其它条目重名的项。
 *
 * 输入：rows 待检测行（顺序即用户看到的顺序）；existing 键为 dirKey 的名字数组
 * 输出：冲突列表；没有冲突时为空数组
 * 预期：建议名同时避开已有名与前面已处理的同批名字；
 *       同批同名只有后一个报冲突，先来的保持原名
 */
export function detectConflicts(
  rows: readonly ConflictRow[],
  existing: Readonly<Record<string, readonly string[]>>
): UploadConflict[] {
  const takenByDir = new Map<string, Set<string>>();
  const conflicts: UploadConflict[] = [];
  for (const row of rows) {
    let taken = takenByDir.get(row.dirKey);
    if (!taken) {
      taken = new Set(existing[row.dirKey] ?? []);
      takenByDir.set(row.dirKey, taken);
    }
    if (taken.has(row.name)) {
      const suggested = uniqueName(row.name, taken);
      conflicts.push({ id: row.id, name: row.name, suggested });
      // 建议名也要占位，否则同批两个同名项会得到同一个建议名
      taken.add(suggested);
    } else {
      taken.add(row.name);
    }
  }
  return conflicts;
}

/**
 * resolveUploadConflicts 按用户选择产出最终清单。
 *
 * 输入：items 原清单；conflicts 冲突列表；choices 每个冲突 id 的动作（缺省按改名处理）
 * 输出：处理后的清单：覆盖保持原名、改名换建议名、跳过整项移除
 * 预期：非冲突项原样保留，顺序不变
 */
export function resolveUploadConflicts(
  items: readonly StagedUpload[],
  conflicts: readonly UploadConflict[],
  choices: Readonly<Record<string, ConflictAction>>
): StagedUpload[] {
  const byId = new Map(conflicts.map((c) => [c.id, c]));
  const out: StagedUpload[] = [];
  for (const item of items) {
    const conflict = byId.get(item.id);
    if (!conflict) {
      out.push(item);
      continue;
    }
    const action = choices[item.id] ?? 'rename';
    if (action === 'skip') continue;
    if (action === 'rename') {
      out.push({ ...item, name: conflict.suggested });
      continue;
    }
    out.push(item);
  }
  return out;
}
