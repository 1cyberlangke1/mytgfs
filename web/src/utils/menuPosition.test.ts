import { describe, expect, it } from 'vitest';
import { menuPosition } from './menuPosition';

// 视口固定 800×600，边距 8、间距 6 是实现的约定值。
const VIEW = { width: 800, height: 600 };

describe('menuPosition', () => {
  it('下方放得下时贴按钮下沿，并右对齐', () => {
    const point = menuPosition(
      { top: 100, right: 500, bottom: 132, left: 400 },
      { width: 200, height: 120 },
      VIEW
    );
    expect(point).toEqual({ left: 300, top: 138 });
  });

  it('下方放不下时翻到按钮上方', () => {
    const point = menuPosition(
      { top: 500, right: 500, bottom: 532, left: 400 },
      { width: 200, height: 120 },
      VIEW
    );
    expect(point).toEqual({ left: 300, top: 374 });
  });

  it('左侧越界时夹到视口内边距', () => {
    const point = menuPosition(
      { top: 100, right: 100, bottom: 132, left: 40 },
      { width: 200, height: 120 },
      { width: 320, height: 600 }
    );
    expect(point.left).toBe(8);
  });

  it('菜单比视口还高时贴顶，交给内部滚动', () => {
    const point = menuPosition(
      { top: 180, right: 300, bottom: 190, left: 200 },
      { width: 200, height: 150 },
      { width: 800, height: 120 }
    );
    expect(point.top).toBe(8);
  });
});
