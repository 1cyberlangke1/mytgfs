import { flushSync } from 'react-dom';

/**
 * runViewTransition 用浏览器的 View Transitions 包裹一次同步 DOM 更新。
 *
 * 输入：update 更新函数（内部改 React 状态）
 * 输出：无
 * 预期：支持时页面在旧/新快照之间做连续过渡；不支持的浏览器直接同步执行，
 *       不会因为缺失 API 而丢更新。flushSync 保证 DOM 在快照前已经落地。
 */
export function runViewTransition(update: () => void) {
  type Doc = Document & { startViewTransition?: (cb: () => void) => unknown };
  const doc = typeof document === 'undefined' ? null : (document as Doc);
  if (!doc || typeof doc.startViewTransition !== 'function') {
    update();
    return;
  }
  doc.startViewTransition(() => flushSync(update));
}
