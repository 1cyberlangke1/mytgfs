import React from 'react';
import { AnimatePresence, motion } from 'motion/react';
import { AlertCircle, CheckCircle2, Loader2, X } from 'lucide-react';
import { IOS_SPRING } from './ui';

export interface ToastMessage {
  id: string;
  type: 'success' | 'error' | 'pending';
  text: string;
  /** 可选操作按钮（如长任务的「取消」）；点一下执行并关掉这条提示 */
  action?: { label: string; onClick: () => void };
}

interface ToastStackProps {
  toasts: ToastMessage[];
  dismissLabel: string;
  onDismiss: (id: string) => void;
  /** 点操作按钮：先执行动作，再关掉这条提示 */
  onAction: (toast: ToastMessage) => void;
}

// ToastStack 是右下角的浮动提示栈。
//
// 输入：当前提示数组、关闭与操作回调
// 输出：每条提示的图标、文案与关闭按钮
// 预期：错误用 alert 立即播报，其余用 status 排队播报；提示本身不阻塞点击
export const ToastStack: React.FC<ToastStackProps> = ({
  toasts,
  dismissLabel,
  onDismiss,
  onAction,
}) => (
  <div className="fixed bottom-20 md:bottom-4 end-4 z-50 flex flex-col gap-2 max-w-md w-[calc(100vw-2rem)] sm:w-auto pointer-events-none">
    <AnimatePresence>
      {toasts.map((toast) => (
        <motion.div
          key={toast.id}
          layout
          initial={{ opacity: 0, y: 16, scale: 0.94 }}
          animate={{ opacity: 1, y: 0, scale: 1 }}
          exit={{ opacity: 0, scale: 0.94, transition: { duration: 0.15 } }}
          transition={IOS_SPRING}
          // 错误用 alert 立即播报，其余用 status 排队播报；
          // 放在每条上而不是外层，避免同一个节点挂两个 live 区重复朗读
          role={toast.type === 'error' ? 'alert' : 'status'}
          className={`pointer-events-auto flex items-center justify-between gap-3 px-4 py-2.5 rounded-2xl shadow-lg border text-xs font-medium backdrop-blur-xl ${
            toast.type === 'error'
              ? 'bg-red-950/90 text-red-100 border-red-800/80'
              : 'bg-zinc-900/90 text-zinc-100 border-white/10'
          }`}
        >
          <div className="flex items-center gap-2.5">
            {toast.type === 'error' ? (
              <AlertCircle className="w-4 h-4 text-red-400 shrink-0" />
            ) : toast.type === 'pending' ? (
              <Loader2 className="w-4 h-4 text-sky-400 shrink-0 animate-spin" />
            ) : (
              <CheckCircle2 className="w-4 h-4 text-emerald-400 shrink-0" />
            )}
            <span className="leading-relaxed">{toast.text}</span>
            {toast.action && (
              <button
                type="button"
                onClick={() => onAction(toast)}
                className="ms-1 px-2.5 py-1 rounded-lg bg-white/10 hover:bg-white/20 text-[11px] font-semibold shrink-0 cursor-pointer"
              >
                {toast.action.label}
              </button>
            )}
          </div>
          <button
            type="button"
            onClick={() => onDismiss(toast.id)}
            aria-label={dismissLabel}
            className="w-7 h-7 rounded-lg inline-flex items-center justify-center text-zinc-400 hover:text-white shrink-0 cursor-pointer"
          >
            <X className="w-3.5 h-3.5" />
          </button>
        </motion.div>
      ))}
    </AnimatePresence>
  </div>
);
