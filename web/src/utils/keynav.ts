// 方向键焦点移动：让列表/卡片组可以用方向键纯键盘走完。
//
// 输入：容器元素、当前焦点、可聚焦项选择器、步长（+1 向下/向右，-1 向上/向左）
// 输出：是否真的移动了焦点
// 预期：挪不动时返回 false，调用方不要 preventDefault，保留浏览器原生滚动；
//       回车/空格交给原生 button/a 处理，这里只管方向键和首尾跳转

// focusableItems 收集容器内可聚焦且可见的项。
//
// 输入：容器、选择器
// 输出：按 DOM 顺序排列的 HTMLElement 数组
// 预期：跳过 disabled、aria-disabled 与不可见的项
function focusableItems(container: HTMLElement, selector: string): HTMLElement[] {
  return Array.from(container.querySelectorAll<HTMLElement>(selector)).filter(
    (el) =>
      !el.hasAttribute('disabled') &&
      el.getAttribute('aria-disabled') !== 'true' &&
      el.checkVisibility()
  );
}

// moveFocusBy 把焦点在容器内的项之间移动一格。
//
// 输入：container 容器；current 当前焦点（可为 null）；selector 项选择器；step 步长
// 输出：移动成功 true；容器为空或当前项已是边界 false
// 预期：current 不在列表里时落在第一项（step>0）或最后一项；到边界不循环、不拦截按键；
//       移动后滚到可见区（nearest 避免整页跳动）
export function moveFocusBy(
  container: HTMLElement | null,
  current: Element | null,
  selector: string,
  step: number
): boolean {
  if (!container) return false;
  const items = focusableItems(container, selector);
  if (items.length === 0) return false;
  const at = current ? items.indexOf(current as HTMLElement) : -1;
  const next = at < 0 ? (step > 0 ? 0 : items.length - 1) : at + step;
  const target = items[next];
  if (!target) return false;
  target.focus();
  target.scrollIntoView({ block: 'nearest', inline: 'nearest' });
  return true;
}

// arrowStep 把方向键换算成列表步长。
//
// 输入：KeyboardEvent.key
// 输出：-1 / 1；不是方向键返回 0
// 预期：纵向列表只认 ↑↓，横向卡片组再加 ←→，由调用方挑选
export function arrowStep(key: string, horizontal = false): number {
  if (key === 'ArrowUp' || (horizontal && key === 'ArrowLeft')) return -1;
  if (key === 'ArrowDown' || (horizontal && key === 'ArrowRight')) return 1;
  return 0;
}

// rovingTabIndex 给组内项分配 tabindex。
//
// 输入：项的索引、当前激活索引
// 输出：0（Tab 能进入的那一项）或 -1（只能用方向键到达）
// 预期：组内只有一个 Tab 停靠点，Tab 键不会在长列表里逐个滚
export function rovingTabIndex(index: number, activeIndex: number): 0 | -1 {
  if (activeIndex >= 0) return index === activeIndex ? 0 : -1;
  return index === 0 ? 0 : -1;
}
