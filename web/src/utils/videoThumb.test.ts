// @vitest-environment jsdom
//
// 缩略图缩放比例的回归测试（纯数学部分，不解码视频）。
//
// 输入：各种原图尺寸
// 输出：thumbScale 的比例与缩放后的像素数
// 预期：比例不超过 1、像素数不超预算、宽高比不变，防止算错把图压坏
import { describe, expect, it } from 'vitest';
import { THUMB_MAX_PIXELS, thumbScale } from './videoThumb';

describe('thumbScale', () => {
  it('小图不放大', () => {
    expect(thumbScale(320, 180)).toBe(1);
    expect(thumbScale(100, 100)).toBe(1);
  });

  it('大图按预算缩到像素上限内', () => {
    const cases: Array<[number, number]> = [
      [3840, 2160],
      [1920, 1080],
      [1080, 1920],
      [4000, 3000],
    ];
    for (const [w, h] of cases) {
      const scale = thumbScale(w, h);
      expect(scale).toBeLessThan(1);
      const tw = Math.max(1, Math.floor(w * scale));
      const th = Math.max(1, Math.floor(h * scale));
      expect(tw * th).toBeLessThanOrEqual(Math.ceil(THUMB_MAX_PIXELS));
      // 宽高比误差控制在 1% 以内
      expect(Math.abs((tw / th) / (w / h) - 1)).toBeLessThan(0.01);
    }
  });

  it('非法尺寸回落到 1', () => {
    expect(thumbScale(0, 1080)).toBe(1);
    expect(thumbScale(1920, 0)).toBe(1);
    expect(thumbScale(-1, -1)).toBe(1);
  });

  it('预算对应 32KB 与 2.5bit/px', () => {
    expect(THUMB_MAX_PIXELS).toBe((32 * 1024 * 8) / 2.5);
  });
});
