import { useCallback, useEffect, useRef, useState } from 'react';
import type { ToastMessage } from '../components/ToastStack';

export type ToastType = 'success' | 'error' | 'pending';
export type ToastAction = { label: string; onClick: () => void };

/** 非 pending 提示的存活时间：足够读完一行字，又不至于一直挡着界面。 */
const TOAST_TTL_MS = 4500;
/** 同时最多停留的提示条数，超出时挤掉最旧的一条。 */
const TOAST_MAX = 4;

export function useToasts() {
  const [toasts, setToasts] = useState<ToastMessage[]>([]);
  const timers = useRef<number[]>([]);

  useEffect(
    () => () => {
      timers.current.forEach((id) => window.clearTimeout(id));
      timers.current = [];
    },
    []
  );

  const pushToast = useCallback(
    (text: string, type: ToastType = 'success', action?: ToastAction): string => {
      const id = `toast-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`;
      setToasts((prev) => [...prev.slice(-(TOAST_MAX - 1)), { id, type, text, ...(action ? { action } : {}) }]);
      if (type !== 'pending') {
        const timer = window.setTimeout(() => {
          timers.current = timers.current.filter((item) => item !== timer);
          setToasts((prev) => prev.filter((item) => item.id !== id));
        }, TOAST_TTL_MS);
        timers.current.push(timer);
      }
      return id;
    },
    []
  );

  const dismissToast = useCallback((id: string) => {
    setToasts((prev) => prev.filter((item) => item.id !== id));
  }, []);

  return { toasts, pushToast, dismissToast };
}
