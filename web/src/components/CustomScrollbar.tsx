import React, { useCallback, useEffect, useRef, useState } from 'react';
import { motion } from 'motion/react';
import { cn, IOS_SPRING } from './ui';

/**
 * useScrollbar 是一套滚动条度量与拖拽逻辑，窗口滚动与局部滚动容器共用。
 *
 * 输入：getScroll 取当前滚动目标的 {scrollHeight, clientHeight, scrollTop}；scrollTo 把目标位置写回去；
 *       minThumb 最小 thumb 高度；inset 轨道上下留白
 * 输出：metrics（是否有溢出、thumb 高度与位移）、isScrolling/isHovered/isDragging 与全部指针回调
 * 预期：滚动事件用 rAF 合并成每帧最多一次；滚动后 1.1s 内保持可见；拖拽按比例映射到 scrollTop
 */
function useScrollbar(
  getScroll: () => { scrollHeight: number; clientHeight: number; scrollTop: number } | null,
  scrollTo: (top: number, smooth?: boolean) => void,
  minThumb: number,
  inset: number
) {
  const trackRef = useRef<HTMLDivElement | null>(null);
  const hideTimerRef = useRef<number | null>(null);
  const rafRef = useRef<number | null>(null);
  const dragRef = useRef({ isDragging: false, startY: 0, startScrollTop: 0 });
  const [metrics, setMetrics] = useState({ hasOverflow: false, thumbHeight: minThumb, thumbTop: 0 });
  const [isScrolling, setIsScrolling] = useState(false);
  const [isHovered, setIsHovered] = useState(false);
  const [isDragging, setIsDragging] = useState(false);

  const updateMetrics = useCallback(() => {
    const s = getScroll();
    if (!s) return;
    const { scrollHeight, clientHeight, scrollTop } = s;
    const hasOverflow = scrollHeight > clientHeight + 4;
    const trackHeight = Math.max(1, clientHeight - inset);
    const ratio = clientHeight / Math.max(1, scrollHeight);
    const thumbHeight = Math.max(minThumb, Math.min(trackHeight, Math.round(trackHeight * ratio)));
    const maxScroll = Math.max(1, scrollHeight - clientHeight);
    const maxThumbTop = Math.max(0, trackHeight - thumbHeight);
    const thumbTop = Math.min(maxThumbTop, Math.max(0, (scrollTop / maxScroll) * maxThumbTop));
    setMetrics({ hasOverflow, thumbHeight, thumbTop });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [getScroll, minThumb, inset]);

  // 滚动事件每帧都会来：用 rAF 合并成「每帧最多算一次」
  const scheduleMetrics = useCallback(() => {
    if (rafRef.current !== null) return;
    rafRef.current = window.requestAnimationFrame(() => {
      rafRef.current = null;
      updateMetrics();
    });
  }, [updateMetrics]);

  const flashScrolling = useCallback(() => {
    setIsScrolling(true);
    if (hideTimerRef.current) window.clearTimeout(hideTimerRef.current);
    hideTimerRef.current = window.setTimeout(() => setIsScrolling(false), 1100);
  }, []);

  const onThumbPointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    e.stopPropagation();
    e.preventDefault();
    e.currentTarget.setPointerCapture(e.pointerId);
    dragRef.current = { isDragging: true, startY: e.clientY, startScrollTop: getScroll()?.scrollTop ?? 0 };
    setIsDragging(true);
  };

  const onThumbPointerMove = (e: React.PointerEvent<HTMLDivElement>) => {
    if (!dragRef.current.isDragging) return;
    e.stopPropagation();
    const s = getScroll();
    if (!s) return;
    const trackHeight = Math.max(1, s.clientHeight - inset);
    const maxThumbTop = Math.max(1, trackHeight - metrics.thumbHeight);
    const maxScroll = Math.max(1, s.scrollHeight - s.clientHeight);
    scrollTo(dragRef.current.startScrollTop + ((e.clientY - dragRef.current.startY) / maxThumbTop) * maxScroll);
  };

  const onThumbPointerUp = (e: React.PointerEvent<HTMLDivElement>) => {
    if (!dragRef.current.isDragging) return;
    e.stopPropagation();
    dragRef.current.isDragging = false;
    setIsDragging(false);
    try {
      e.currentTarget.releasePointerCapture(e.pointerId);
    } catch {
      // 指针已被释放：忽略
    }
  };

  // 点轨道空白处：把点击位置当成 thumb 中心，滚过去
  const onTrackPointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    if (e.target !== trackRef.current) return;
    const track = trackRef.current;
    const s = getScroll();
    if (!track || !s) return;
    const rect = track.getBoundingClientRect();
    const ratio = Math.max(0, Math.min(1, (e.clientY - rect.top - metrics.thumbHeight / 2) / Math.max(1, rect.height - metrics.thumbHeight)));
    scrollTo(ratio * Math.max(0, s.scrollHeight - s.clientHeight), true);
  };

  useEffect(() => {
    updateMetrics();
    return () => {
      if (rafRef.current !== null) window.cancelAnimationFrame(rafRef.current);
      if (hideTimerRef.current) window.clearTimeout(hideTimerRef.current);
    };
  }, [updateMetrics]);

  return {
    trackRef, metrics, isScrolling, isHovered, isDragging, scheduleMetrics, flashScrolling,
    setIsHovered, onThumbPointerDown, onThumbPointerMove, onThumbPointerUp, onTrackPointerDown,
  };
}

/**
 * WindowCustomScrollbar 是窗口级浮动滚动条。
 *
 * 输入：无（读 document/window 的滚动尺寸）
 * 输出：固定在右侧的轨道与 thumb；无溢出时不渲染
 * 预期：thumb 宽度与透明度按 拖拽 > 悬停 > 滚动中 > 静止 四档变化；拖拽时变蓝
 */
export const WindowCustomScrollbar: React.FC = () => {
  const bar = useScrollbar(
    useCallback(() => {
      const doc = document.documentElement;
      return {
        scrollHeight: Math.max(doc.scrollHeight, document.body.scrollHeight),
        clientHeight: window.innerHeight,
        scrollTop: window.scrollY || doc.scrollTop,
      };
    }, []),
    (top, smooth) => window.scrollTo({ top, behavior: smooth ? 'smooth' : ('instant' as ScrollBehavior) }),
    36,
    16
  );

  useEffect(() => {
    const onScroll = () => {
      bar.scheduleMetrics();
      bar.flashScrolling();
    };
    const onResize = () => bar.scheduleMetrics();
    window.addEventListener('scroll', onScroll, { passive: true });
    window.addEventListener('resize', onResize, { passive: true });
    const resizeObserver = new ResizeObserver(() => bar.scheduleMetrics());
    resizeObserver.observe(document.documentElement);
    resizeObserver.observe(document.body);
    return () => {
      window.removeEventListener('scroll', onScroll);
      window.removeEventListener('resize', onResize);
      resizeObserver.disconnect();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [bar.scheduleMetrics, bar.flashScrolling]);

  if (!bar.metrics.hasOverflow) return null;
  const active = bar.isDragging || bar.isHovered || bar.isScrolling;
  return (
    <div
      ref={bar.trackRef}
      aria-hidden="true"
      data-testid="custom-window-scrollbar"
      onPointerEnter={() => bar.setIsHovered(true)}
      onPointerLeave={() => bar.setIsHovered(false)}
      onPointerDown={bar.onTrackPointerDown}
      className={cn(
        'fixed top-2 bottom-20 md:bottom-2 end-1 z-50 w-3.5 flex justify-end select-none transition-colors rounded-full',
        bar.isHovered || bar.isDragging ? 'bg-zinc-500/10 dark:bg-white/[0.06] cursor-pointer' : 'bg-transparent'
      )}
    >
      <motion.div
        data-testid="custom-window-scrollbar-thumb"
        onPointerDown={bar.onThumbPointerDown}
        onPointerMove={bar.onThumbPointerMove}
        onPointerUp={bar.onThumbPointerUp}
        onPointerCancel={bar.onThumbPointerUp}
        animate={{
          width: bar.isDragging ? 9 : bar.isHovered ? 8 : 5,
          opacity: bar.isDragging ? 1 : bar.isHovered ? 0.92 : active ? 0.68 : 0.35,
        }}
        transition={IOS_SPRING}
        style={{ height: `${bar.metrics.thumbHeight}px`, transform: `translate3d(0, ${bar.metrics.thumbTop}px, 0)` }}
        className={cn(
          'rounded-full cursor-grab active:cursor-grabbing backdrop-blur-md transition-colors shadow-2xs',
          bar.isDragging
            ? 'bg-[#007AFF] dark:bg-[#0A84FF]'
            : bar.isHovered
              ? 'bg-zinc-600/85 dark:bg-zinc-300/80'
              : 'bg-zinc-500/60 dark:bg-zinc-400/50'
        )}
      />
    </div>
  );
};

export interface CustomScrollAreaProps extends React.HTMLAttributes<HTMLDivElement> {
  children: React.ReactNode;
  viewportClassName?: string;
}

/**
 * CustomScrollArea 是局部滚动容器，滚动条绘制与窗口版共用同一套逻辑。
 *
 * 输入：children 内容；className 外层类；viewportClassName 滚动视口类
 * 输出：可滚动视口 + 覆盖式滚动条；无溢出时不渲染滚动条
 * 预期：滚动条不占布局宽度（绝对定位）；内容用 flex 撑满最少一屏
 */
export const CustomScrollArea: React.FC<CustomScrollAreaProps> = ({ children, className, viewportClassName, ...props }) => {
  const viewportRef = useRef<HTMLDivElement | null>(null);
  const bar = useScrollbar(
    useCallback(() => {
      const el = viewportRef.current;
      if (!el) return null;
      return { scrollHeight: el.scrollHeight, clientHeight: el.clientHeight, scrollTop: el.scrollTop };
    }, []),
    (top, smooth) => {
      const el = viewportRef.current;
      if (el) el.scrollTo({ top, behavior: smooth ? 'smooth' : 'auto' });
    },
    28,
    12
  );

  useEffect(() => {
    const el = viewportRef.current;
    if (!el) return;
    const onScroll = () => {
      bar.scheduleMetrics();
      bar.flashScrolling();
    };
    el.addEventListener('scroll', onScroll, { passive: true });
    const resizeObserver = new ResizeObserver(() => bar.scheduleMetrics());
    resizeObserver.observe(el);
    if (el.firstElementChild) resizeObserver.observe(el.firstElementChild);
    return () => {
      el.removeEventListener('scroll', onScroll);
      resizeObserver.disconnect();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [bar.scheduleMetrics, bar.flashScrolling]);

  return (
    <div
      onPointerEnter={() => bar.setIsHovered(true)}
      onPointerLeave={() => bar.setIsHovered(false)}
      className={cn('relative overflow-hidden', className)}
      {...props}
    >
      <div ref={viewportRef} className={cn('h-full w-full overflow-y-auto overflow-x-hidden', viewportClassName)}>
        <div className="min-h-full flex flex-col justify-between">{children}</div>
      </div>

      {bar.metrics.hasOverflow && (
        <div
          ref={bar.trackRef}
          aria-hidden="true"
          data-testid="custom-area-scrollbar"
          onPointerDown={bar.onTrackPointerDown}
          className={cn(
            'absolute top-1.5 bottom-1.5 end-0.5 z-30 w-2.5 flex justify-end select-none rounded-full transition-colors',
            bar.isHovered || bar.isDragging ? 'bg-zinc-500/10 dark:bg-white/[0.05] cursor-pointer' : 'bg-transparent'
          )}
        >
          <motion.div
            data-testid="custom-area-scrollbar-thumb"
            onPointerDown={bar.onThumbPointerDown}
            onPointerMove={bar.onThumbPointerMove}
            onPointerUp={bar.onThumbPointerUp}
            onPointerCancel={bar.onThumbPointerUp}
            animate={{
              width: bar.isDragging ? 7 : bar.isHovered ? 6 : 4,
              opacity: bar.isDragging ? 1 : bar.isHovered ? 0.9 : bar.isScrolling ? 0.72 : 0.32,
            }}
            transition={IOS_SPRING}
            style={{ height: `${bar.metrics.thumbHeight}px`, transform: `translate3d(0, ${bar.metrics.thumbTop}px, 0)` }}
            className={cn(
              'rounded-full cursor-grab active:cursor-grabbing transition-colors',
              bar.isDragging
                ? 'bg-[#007AFF] dark:bg-[#0A84FF]'
                : bar.isHovered
                  ? 'bg-zinc-600/85 dark:bg-zinc-300/80'
                  : 'bg-zinc-500/55 dark:bg-zinc-400/45'
            )}
          />
        </div>
      )}
    </div>
  );
};
