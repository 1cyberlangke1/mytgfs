// 弹出菜单的定位计算。
//
// 输入：触发按钮矩形、菜单尺寸、视口尺寸
// 输出：菜单左上角坐标（视口坐标，配合 position: fixed 使用）
// 预期：
//   - 优先贴触发按钮下方，右边缘对齐触发按钮右边缘（工具栏靠右，左对齐更容易顶出视口）
//   - 下方放不下就翻到上方；上下都放不下时贴底并夹在视口内
//   - 左右始终留出 EDGE 的内边距，绝不被视口裁掉

/** 视口内边距：菜单离屏幕边缘至少这么远 */
const EDGE = 8;
/** 菜单与触发按钮之间的间距 */
const GAP = 6;

export interface AnchorRect {
  top: number;
  right: number;
  bottom: number;
  left: number;
}

export interface BoxSize {
  width: number;
  height: number;
}

export interface Point {
  left: number;
  top: number;
}

export function menuPosition(
  anchor: AnchorRect,
  menu: BoxSize,
  viewport: BoxSize
): Point {
  // 右对齐到触发按钮；再夹进视口左右边界
  let left = Math.min(anchor.right - menu.width, viewport.width - EDGE - menu.width);
  if (left < EDGE) left = EDGE;

  let top = anchor.bottom + GAP;
  if (top + menu.height > viewport.height - EDGE) {
    const above = anchor.top - GAP - menu.height;
    top = above >= EDGE ? above : Math.max(EDGE, viewport.height - EDGE - menu.height);
  }
  // 菜单比可视区还高时上面这条仍会溢出，统一夹一次，宁可内部滚动也不越过边距
  top = Math.max(EDGE, Math.min(top, viewport.height - EDGE - menu.height));

  return { left: Math.round(left), top: Math.round(top) };
}
