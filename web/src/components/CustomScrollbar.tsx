import React, { useCallback, useEffect, useRef, useState } from 'react';
import { motion } from 'motion/react';
import { cn, IOS_SPRING } from './ui';

/**
 * Custom iOS / macOS floating overlay scrollbar for the main window viewport.
 * - Replaces the native OS scrollbar with a custom interactive floating pill.
 * - Supports thumb dragging (with pointer capture), track click-to-scroll,
 *   hover expansion (5px -> 9px), and active scroll auto-reveal.
 * - RTL-aware (`end-1`).
 */
export const WindowCustomScrollbar: React.FC = () => {
  const trackRef = useRef<HTMLDivElement | null>(null);
  const hideTimerRef = useRef<number | null>(null);
  const dragStateRef = useRef<{
    isDragging: boolean;
    startY: number;
    startScrollTop: number;
  }>({
    isDragging: false,
    startY: 0,
    startScrollTop: 0,
  });

  const [metrics, setMetrics] = useState({
    hasOverflow: false,
    thumbHeight: 48,
    thumbTop: 0,
  });
  const [isScrolling, setIsScrolling] = useState(false);
  const [isHovered, setIsHovered] = useState(false);
  const [isDragging, setIsDragging] = useState(false);

  const updateMetrics = useCallback(() => {
    const doc = document.documentElement;
    const scrollHeight = Math.max(doc.scrollHeight, document.body.scrollHeight);
    const clientHeight = window.innerHeight;
    const scrollTop = window.scrollY || doc.scrollTop;

    const hasOverflow = scrollHeight > clientHeight + 4;
    const trackHeight = Math.max(1, clientHeight - 16); // 8px top + 8px bottom margin
    const ratio = clientHeight / Math.max(1, scrollHeight);
    const thumbHeight = Math.max(36, Math.min(trackHeight, Math.round(trackHeight * ratio)));
    const maxScroll = Math.max(1, scrollHeight - clientHeight);
    const maxThumbTop = Math.max(0, trackHeight - thumbHeight);
    const thumbTop = Math.min(maxThumbTop, Math.max(0, (scrollTop / maxScroll) * maxThumbTop));

    setMetrics({
      hasOverflow,
      thumbHeight,
      thumbTop,
    });
  }, []);

  const flashScrolling = useCallback(() => {
    setIsScrolling(true);
    if (hideTimerRef.current) window.clearTimeout(hideTimerRef.current);
    hideTimerRef.current = window.setTimeout(() => {
      setIsScrolling(false);
    }, 1100);
  }, []);

  useEffect(() => {
    updateMetrics();

    const handleScroll = () => {
      updateMetrics();
      flashScrolling();
    };

    const handleResize = () => {
      updateMetrics();
    };

    window.addEventListener('scroll', handleScroll, { passive: true });
    window.addEventListener('resize', handleResize, { passive: true });

    const resizeObserver = new ResizeObserver(() => {
      updateMetrics();
    });
    resizeObserver.observe(document.documentElement);
    resizeObserver.observe(document.body);

    return () => {
      window.removeEventListener('scroll', handleScroll);
      window.removeEventListener('resize', handleResize);
      resizeObserver.disconnect();
      if (hideTimerRef.current) window.clearTimeout(hideTimerRef.current);
    };
  }, [updateMetrics, flashScrolling]);

  const handleThumbPointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    e.stopPropagation();
    e.preventDefault();
    e.currentTarget.setPointerCapture(e.pointerId);
    dragStateRef.current = {
      isDragging: true,
      startY: e.clientY,
      startScrollTop: window.scrollY || document.documentElement.scrollTop,
    };
    setIsDragging(true);
  };

  const handleThumbPointerMove = (e: React.PointerEvent<HTMLDivElement>) => {
    if (!dragStateRef.current.isDragging) return;
    e.stopPropagation();
    const doc = document.documentElement;
    const scrollHeight = Math.max(doc.scrollHeight, document.body.scrollHeight);
    const clientHeight = window.innerHeight;
    const trackHeight = Math.max(1, clientHeight - 16);
    const maxThumbTop = Math.max(1, trackHeight - metrics.thumbHeight);
    const maxScroll = Math.max(1, scrollHeight - clientHeight);

    const deltaY = e.clientY - dragStateRef.current.startY;
    const scrollDelta = (deltaY / maxThumbTop) * maxScroll;
    window.scrollTo({
      top: dragStateRef.current.startScrollTop + scrollDelta,
      behavior: 'instant' as ScrollBehavior,
    });
  };

  const handleThumbPointerUp = (e: React.PointerEvent<HTMLDivElement>) => {
    if (!dragStateRef.current.isDragging) return;
    e.stopPropagation();
    dragStateRef.current.isDragging = false;
    setIsDragging(false);
    try {
      e.currentTarget.releasePointerCapture(e.pointerId);
    } catch {
      // ignore if already released
    }
  };

  const handleTrackPointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    if (e.target !== trackRef.current) return;
    const rect = trackRef.current.getBoundingClientRect();
    const clickY = e.clientY - rect.top;
    const doc = document.documentElement;
    const scrollHeight = Math.max(doc.scrollHeight, document.body.scrollHeight);
    const clientHeight = window.innerHeight;
    const trackHeight = Math.max(1, rect.height);
    const targetRatio = Math.max(
      0,
      Math.min(1, (clickY - metrics.thumbHeight / 2) / Math.max(1, trackHeight - metrics.thumbHeight))
    );
    window.scrollTo({
      top: targetRatio * Math.max(0, scrollHeight - clientHeight),
      behavior: 'smooth',
    });
  };

  if (!metrics.hasOverflow) return null;

  const isActive = isDragging || isHovered || isScrolling;

  return (
    <div
      ref={trackRef}
      aria-hidden="true"
      data-testid="custom-window-scrollbar"
      onPointerEnter={() => setIsHovered(true)}
      onPointerLeave={() => setIsHovered(false)}
      onPointerDown={handleTrackPointerDown}
      className={cn(
        'fixed top-2 bottom-20 md:bottom-2 end-1 z-50 w-3.5 flex justify-end select-none transition-colors rounded-full',
        isHovered || isDragging
          ? 'bg-zinc-500/10 dark:bg-white/[0.06] cursor-pointer'
          : 'bg-transparent'
      )}
    >
      <motion.div
        data-testid="custom-window-scrollbar-thumb"
        onPointerDown={handleThumbPointerDown}
        onPointerMove={handleThumbPointerMove}
        onPointerUp={handleThumbPointerUp}
        onPointerCancel={handleThumbPointerUp}
        animate={{
          width: isDragging ? 9 : isHovered ? 8 : 5,
          opacity: isDragging ? 1 : isHovered ? 0.92 : isActive ? 0.68 : 0.35,
        }}
        transition={IOS_SPRING}
        style={{
          height: `${metrics.thumbHeight}px`,
          transform: `translate3d(0, ${metrics.thumbTop}px, 0)`,
        }}
        className={cn(
          'rounded-full cursor-grab active:cursor-grabbing backdrop-blur-md transition-colors shadow-2xs',
          isDragging
            ? 'bg-[#007AFF] dark:bg-[#0A84FF]'
            : isHovered
            ? 'bg-zinc-600/85 dark:bg-zinc-300/80'
            : 'bg-zinc-500/60 dark:bg-zinc-400/50'
        )}
      />
    </div>
  );
};

interface CustomScrollAreaProps extends React.HTMLAttributes<HTMLDivElement> {
  children: React.ReactNode;
  viewportClassName?: string;
}

/**
 * Custom Scrollable Container with its own sleek floating iOS/macOS scrollbar pill.
 * Used for the Sidebar, Modals, and Inner Panels so native scrollbars never appear.
 */
export const CustomScrollArea: React.FC<CustomScrollAreaProps> = ({
  children,
  className,
  viewportClassName,
  ...props
}) => {
  const viewportRef = useRef<HTMLDivElement | null>(null);
  const contentRef = useRef<HTMLDivElement | null>(null);
  const trackRef = useRef<HTMLDivElement | null>(null);
  const hideTimerRef = useRef<number | null>(null);
  const dragStateRef = useRef<{
    isDragging: boolean;
    startY: number;
    startScrollTop: number;
  }>({
    isDragging: false,
    startY: 0,
    startScrollTop: 0,
  });

  const [metrics, setMetrics] = useState({
    hasOverflow: false,
    thumbHeight: 36,
    thumbTop: 0,
  });
  const [isScrolling, setIsScrolling] = useState(false);
  const [isHovered, setIsHovered] = useState(false);
  const [isDragging, setIsDragging] = useState(false);

  const updateMetrics = useCallback(() => {
    const el = viewportRef.current;
    if (!el) return;
    const { scrollHeight, clientHeight, scrollTop } = el;
    const hasOverflow = scrollHeight > clientHeight + 3;
    const trackHeight = Math.max(1, clientHeight - 12);
    const ratio = clientHeight / Math.max(1, scrollHeight);
    const thumbHeight = Math.max(28, Math.min(trackHeight, Math.round(trackHeight * ratio)));
    const maxScroll = Math.max(1, scrollHeight - clientHeight);
    const maxThumbTop = Math.max(0, trackHeight - thumbHeight);
    const thumbTop = Math.min(maxThumbTop, Math.max(0, (scrollTop / maxScroll) * maxThumbTop));

    setMetrics({
      hasOverflow,
      thumbHeight,
      thumbTop,
    });
  }, []);

  const flashScrolling = useCallback(() => {
    setIsScrolling(true);
    if (hideTimerRef.current) window.clearTimeout(hideTimerRef.current);
    hideTimerRef.current = window.setTimeout(() => {
      setIsScrolling(false);
    }, 1000);
  }, []);

  useEffect(() => {
    const el = viewportRef.current;
    const inner = contentRef.current;
    if (!el) return;

    updateMetrics();

    const handleScroll = () => {
      updateMetrics();
      flashScrolling();
    };

    el.addEventListener('scroll', handleScroll, { passive: true });
    const resizeObserver = new ResizeObserver(() => {
      updateMetrics();
    });
    resizeObserver.observe(el);
    if (inner) resizeObserver.observe(inner);

    return () => {
      el.removeEventListener('scroll', handleScroll);
      resizeObserver.disconnect();
      if (hideTimerRef.current) window.clearTimeout(hideTimerRef.current);
    };
  }, [updateMetrics, flashScrolling]);

  const handleThumbPointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    e.stopPropagation();
    e.preventDefault();
    const el = viewportRef.current;
    if (!el) return;
    e.currentTarget.setPointerCapture(e.pointerId);
    dragStateRef.current = {
      isDragging: true,
      startY: e.clientY,
      startScrollTop: el.scrollTop,
    };
    setIsDragging(true);
  };

  const handleThumbPointerMove = (e: React.PointerEvent<HTMLDivElement>) => {
    if (!dragStateRef.current.isDragging) return;
    e.stopPropagation();
    const el = viewportRef.current;
    if (!el) return;
    const { scrollHeight, clientHeight } = el;
    const trackHeight = Math.max(1, clientHeight - 12);
    const maxThumbTop = Math.max(1, trackHeight - metrics.thumbHeight);
    const maxScroll = Math.max(1, scrollHeight - clientHeight);

    const deltaY = e.clientY - dragStateRef.current.startY;
    el.scrollTop = dragStateRef.current.startScrollTop + (deltaY / maxThumbTop) * maxScroll;
  };

  const handleThumbPointerUp = (e: React.PointerEvent<HTMLDivElement>) => {
    if (!dragStateRef.current.isDragging) return;
    e.stopPropagation();
    dragStateRef.current.isDragging = false;
    setIsDragging(false);
    try {
      e.currentTarget.releasePointerCapture(e.pointerId);
    } catch {
      // ignore
    }
  };

  const handleTrackPointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    if (e.target !== trackRef.current) return;
    const el = viewportRef.current;
    const track = trackRef.current;
    if (!el || !track) return;
    const rect = track.getBoundingClientRect();
    const clickY = e.clientY - rect.top;
    const targetRatio = Math.max(
      0,
      Math.min(1, (clickY - metrics.thumbHeight / 2) / Math.max(1, rect.height - metrics.thumbHeight))
    );
    el.scrollTo({
      top: targetRatio * Math.max(0, el.scrollHeight - el.clientHeight),
      behavior: 'smooth',
    });
  };

  return (
    <div
      onPointerEnter={() => setIsHovered(true)}
      onPointerLeave={() => setIsHovered(false)}
      className={cn('relative overflow-hidden', className)}
      {...props}
    >
      <div
        ref={viewportRef}
        className={cn('h-full w-full overflow-y-auto overflow-x-hidden', viewportClassName)}
      >
        <div ref={contentRef} className="min-h-full flex flex-col justify-between">
          {children}
        </div>
      </div>

      {metrics.hasOverflow && (
        <div
          ref={trackRef}
          aria-hidden="true"
          data-testid="custom-area-scrollbar"
          onPointerDown={handleTrackPointerDown}
          className={cn(
            'absolute top-1.5 bottom-1.5 end-0.5 z-30 w-2.5 flex justify-end select-none rounded-full transition-colors',
            isHovered || isDragging
              ? 'bg-zinc-500/10 dark:bg-white/[0.05] cursor-pointer'
              : 'bg-transparent'
          )}
        >
          <motion.div
            data-testid="custom-area-scrollbar-thumb"
            onPointerDown={handleThumbPointerDown}
            onPointerMove={handleThumbPointerMove}
            onPointerUp={handleThumbPointerUp}
            onPointerCancel={handleThumbPointerUp}
            animate={{
              width: isDragging ? 7 : isHovered ? 6 : 4,
              opacity: isDragging ? 1 : isHovered ? 0.9 : isScrolling ? 0.72 : 0.32,
            }}
            transition={IOS_SPRING}
            style={{
              height: `${metrics.thumbHeight}px`,
              transform: `translate3d(0, ${metrics.thumbTop}px, 0)`,
            }}
            className={cn(
              'rounded-full cursor-grab active:cursor-grabbing transition-colors',
              isDragging
                ? 'bg-[#007AFF] dark:bg-[#0A84FF]'
                : isHovered
                ? 'bg-zinc-600/85 dark:bg-zinc-300/80'
                : 'bg-zinc-500/55 dark:bg-zinc-400/45'
            )}
          />
        </div>
      )}
    </div>
  );
};
