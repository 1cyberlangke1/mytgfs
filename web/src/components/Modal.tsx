import { useEffect, useRef, type ReactNode } from 'react';

/**
 * 原生 <dialog> 承载弹窗。
 *
 * 输入：open 是否显示；onClose 关闭回调；labelledBy 标题元素 id；panelClassName 内层面板样式
 * 输出：top-layer 弹窗
 * 预期：inert 背景、焦点限制与归还、Esc 与浏览器关闭手势都由浏览器负责，
 *       调用方不再手写 role="dialog"/aria-modal/Esc 监听；
 *       点遮罩关闭用「按下与抬起都落在 dialog 自身」判定，拖选文字不会误关。
 */
export interface ModalProps {
  /** 默认 true：组件挂载即打开，由调用方条件渲染控制生命周期 */
  open?: boolean;
  onClose: () => void;
  labelledBy: string;
  panelClassName?: string;
  /** true 时忽略 Esc 与点遮罩，用于提交中不允许关闭 */
  closeDisabled?: boolean;
  children: ReactNode;
}

export function Modal({
  open = true,
  onClose,
  labelledBy,
  panelClassName,
  closeDisabled = false,
  children,
}: ModalProps) {
  const ref = useRef<HTMLDialogElement>(null);
  const openRef = useRef(open);
  openRef.current = open;
  const disabledRef = useRef(closeDisabled);
  disabledRef.current = closeDisabled;

  // 提交中按 Esc：拦掉浏览器默认的关闭手势
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const onCancel = (e: Event) => {
      if (disabledRef.current) e.preventDefault();
    };
    el.addEventListener('cancel', onCancel);
    return () => el.removeEventListener('cancel', onCancel);
  }, []);

  // 打开/关闭与 React 状态同步
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    if (open && !el.open) {
      if (typeof el.showModal === 'function') {
        el.showModal();
      } else {
        el.setAttribute('open', '');
      }
      // 显式尊重 autofocus：showModal 之后再聚焦，避免落在关闭/惰性状态上
      el.querySelector<HTMLElement>('[autofocus]')?.focus();
    } else if (!open && el.open) {
      el.close();
    }
  }, [open]);

  // Esc/关闭手势触发 close 事件：把状态收回 React，避免父组件以为还开着
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const onCloseEvent = () => {
      if (openRef.current) onClose();
    };
    el.addEventListener('close', onCloseEvent);
    return () => el.removeEventListener('close', onCloseEvent);
  }, [onClose]);

  // 点遮罩关闭：只有目标就是 dialog 自身（背景与内边距）才算，
  // 且按下与抬起都必须在背景上，避免从面板里拖选到外面误关
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    let pressedOnBackdrop = false;
    const down = (e: PointerEvent) => {
      pressedOnBackdrop = e.target === el;
    };
    const up = (e: PointerEvent) => {
      if (!disabledRef.current && pressedOnBackdrop && e.target === el) el.close();
      pressedOnBackdrop = false;
    };
    el.addEventListener('pointerdown', down);
    el.addEventListener('pointerup', up);
    return () => {
      el.removeEventListener('pointerdown', down);
      el.removeEventListener('pointerup', up);
    };
  }, []);

  return (
    <dialog ref={ref} aria-labelledby={labelledBy} className="modal-shell">
      <div className={panelClassName ? `my-auto ${panelClassName}` : 'my-auto'}>{children}</div>
    </dialog>
  );
}
