// 空间键盘导航：方向键在可见控件之间按屏幕位置移动焦点。
//
// 输入：document（侧栏 + 主内容）与上一次焦点位置
// 输出：是否真的移动了焦点
// 预期：挪不动时返回 false，调用方不拦截按键，保留浏览器原生滚动；
//       只用真实布局几何，不依赖 DOM 顺序，网格/换行/两栏都能走对方向

const FOCUSABLE =
  'a[href], button, input, select, textarea, summary, [tabindex]:not([tabindex="-1"])';

export type Direction = 'up' | 'down' | 'left' | 'right';

// candidates 收集当前可见、可聚焦、未禁用的控件。
//
// 输入：无（扫全文档，侧栏与主内容同一坐标系）
// 输出：HTMLElement 数组
// 预期：隐藏元素、禁用项、inert 子树里的项都不参与导航
function candidates(): HTMLElement[] {
  return Array.from(document.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
    (el) =>
      !el.hasAttribute('disabled') &&
      el.getAttribute('aria-disabled') !== 'true' &&
      !el.closest('[inert]') &&
      el.checkVisibility()
  );
}

// focusPrimaryNav 把焦点交给当前页签的导航项。
//
// 输入：无
// 输出：是否找到可见的导航项
// 预期：桌面侧栏与手机底栏同时存在时，只取当前可见的那一组
export function focusPrimaryNav(): boolean {
  const links = Array.from(document.querySelectorAll<HTMLAnchorElement>('nav[aria-label] a')).filter(
    (a) => a.checkVisibility()
  );
  const target = links.find((a) => a.getAttribute('aria-current') === 'page') ?? links[0];
  if (!target) return false;
  target.focus();
  target.scrollIntoView({ block: 'nearest', inline: 'nearest' });
  return true;
}

// moveFocusDirection 按方向找最近的可见控件并聚焦。
//
// 输入：方向
// 输出：移动成功 true；没有候选 false
// 预期：方向判定用边缘比较（允许 1px 误差）；优先与当前项垂直/水平重叠的候选，
//       重叠候选按轴向间距排序，其次按垂直轴中心偏移加权
export function moveFocusDirection(dir: Direction): boolean {
  const current = document.activeElement as HTMLElement | null;
  if (!current || current === document.body) return focusPrimaryNav();
  const cur = current.getBoundingClientRect();
  if (cur.width === 0 && cur.height === 0) return false;

  const horizontal = dir === 'left' || dir === 'right';
  let best: HTMLElement | null = null;
  let bestScore = Infinity;

  for (const el of candidates()) {
    if (el === current || el.contains(current)) continue;
    const r = el.getBoundingClientRect();
    if (r.width === 0 || r.height === 0) continue;

    const tol = 1;
    let gap: number;
    if (dir === 'down') {
      if (r.top < cur.bottom - tol) continue;
      gap = r.top - cur.bottom;
    } else if (dir === 'up') {
      if (r.bottom > cur.top + tol) continue;
      gap = cur.top - r.bottom;
    } else if (dir === 'right') {
      if (r.left < cur.right - tol) continue;
      gap = r.left - cur.right;
    } else {
      if (r.right > cur.left + tol) continue;
      gap = cur.left - r.right;
    }

    const overlap = horizontal
      ? Math.min(r.bottom, cur.bottom) - Math.max(r.top, cur.top)
      : Math.min(r.right, cur.right) - Math.max(r.left, cur.left);
    const perpOffset = horizontal
      ? Math.abs((r.top + r.bottom) / 2 - (cur.top + cur.bottom) / 2)
      : Math.abs((r.left + r.right) / 2 - (cur.left + cur.right) / 2);

    // 同行/同列优先：重叠时不额外惩罚，靠间隙决定；不重叠才用偏移加权
    const score = overlap > 0 ? gap + perpOffset * 0.05 : gap + perpOffset * 3;
    if (score < bestScore) {
      bestScore = score;
      best = el;
    }
  }

  if (!best) return false;
  best.focus();
  best.scrollIntoView({ block: 'nearest', inline: 'nearest' });
  return true;
}

// scrollContainer 找当前元素所在的滚动容器。
//
// 输入：起始元素
// 输出：可滚动的祖先；没有则返回 null
// 预期：只认真正可滚动的元素，避免拿 body 造成整页跳
function scrollContainer(from: Element | null): HTMLElement | null {
  let el = from as HTMLElement | null;
  while (el && el !== document.body) {
    if (el.scrollHeight > el.clientHeight + 4 && el.scrollTop !== undefined) return el;
    el = el.parentElement;
  }
  return null;
}

// scrollActiveByPage 让当前焦点所在区域翻一屏。
//
// 输入：方向（1 向下，-1 向上）
// 输出：是否发生滚动
// 预期：没有可滚动祖先时不动，交给浏览器默认行为
export function scrollActiveByPage(dir: 1 | -1): boolean {
  const box = scrollContainer(document.activeElement);
  if (!box) return false;
  const step = Math.max(80, box.clientHeight - 48) * dir;
  box.scrollBy({ top: step, behavior: 'auto' });
  return true;
}

// scrollActiveToEdge 把当前焦点所在区域滚到顶部或底部。
//
// 输入：'start' 顶部，'end' 底部
// 输出：是否发生滚动
// 预期：没有滚动容器时不动
export function scrollActiveToEdge(edge: 'start' | 'end'): boolean {
  const box = scrollContainer(document.activeElement);
  if (!box) return false;
  box.scrollTo({ top: edge === 'start' ? 0 : box.scrollHeight, behavior: 'auto' });
  return true;
}

// activateActive 让开关类控件响应回车。
//
// 输入：无（读 document.activeElement）
// 输出：是否处理（true 表示这是个开关/复选，已切换）
// 预期：原生按钮与链接不在此处理，它们本身回车就生效
export function activateActive(): boolean {
  const el = document.activeElement as HTMLElement | null;
  if (!el) return false;
  if (el.getAttribute('role') === 'switch' || el.getAttribute('role') === 'checkbox') {
    el.click();
    return true;
  }
  if (el instanceof HTMLInputElement && (el.type === 'checkbox' || el.type === 'radio')) {
    el.click();
    return true;
  }
  return false;
}
