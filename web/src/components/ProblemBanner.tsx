import React from 'react';
import { AnimatePresence, motion } from 'motion/react';
import { AlertCircle, X } from 'lucide-react';
import { Button, IOS_SOFT_SPRING } from './ui';

interface ProblemBannerProps {
  /** 每条待展示的失败原因（已本地化） */
  messages: string[];
  title: string;
  retryLabel: string;
  dismissLabel: string;
  onRetry: () => void;
  onDismiss: () => void;
}

// ProblemBanner 是常驻错误横幅：后台加载失败或推送断开时一直显示，恢复或手动关闭才消失。
export const ProblemBanner: React.FC<ProblemBannerProps> = ({
  messages,
  title,
  retryLabel,
  dismissLabel,
  onRetry,
  onDismiss,
}) => (
  <AnimatePresence initial={false}>
    {messages.length > 0 && (
      <motion.div
        role="alert"
        initial={{ opacity: 0, y: -8 }}
        animate={{ opacity: 1, y: 0 }}
        exit={{ opacity: 0, y: -8 }}
        transition={IOS_SOFT_SPRING}
        className="mb-4 rounded-2xl border border-red-300/70 dark:border-red-900/70 bg-red-50/95 dark:bg-red-950/60 px-4 py-3"
      >
        <div className="flex items-start gap-2.5">
          <AlertCircle className="w-4 h-4 mt-0.5 shrink-0 text-red-600 dark:text-red-400" />
          <div className="min-w-0 flex-1 space-y-1">
            <p className="text-xs font-semibold text-red-700 dark:text-red-300">{title}</p>
            {messages.map((msg) => (
              <p
                key={msg}
                className="text-[11px] leading-relaxed text-red-700/90 dark:text-red-200/90 break-words"
              >
                {msg}
              </p>
            ))}
          </div>
          <div className="flex items-center gap-1 shrink-0">
            <Button type="button" variant="outline" size="sm" onClick={onRetry}>
              {retryLabel}
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              onClick={onDismiss}
              aria-label={dismissLabel}
            >
              <X className="w-4 h-4" />
            </Button>
          </div>
        </div>
      </motion.div>
    )}
  </AnimatePresence>
);
