// 前端唯一的推送入口：桌面壳走 Wails 原生事件，网页版走 SSE。
//
// 主题名与 Go 侧 internal/push 的 Topics 一一对应；加主题只改这两处，
// 不允许在组件里再写一套 EventSource / Events.On 分支。

export type PushTopic = 'status' | 'config';

const TOPICS: PushTopic[] = ['status', 'config'];

// desktopEvent 与 Go 侧 push.DesktopEvent 必须给出相同字符串。
const desktopEvent = (topic: PushTopic) => `mytgfs:${topic}`;

// Wails 运行时的最小接口；只有 On 是这里需要的。
interface WailsEvents {
  On(name: string, cb: () => void): () => void;
}

// 桌面壳运行时：main.tsx 在渲染前把 /wails/runtime.js 挂到 window.wails。
function desktopEvents(): WailsEvents | undefined {
  return (window as unknown as { wails?: { Events?: WailsEvents } }).wails?.Events;
}

// initPushRuntime 在桌面壳里加载 Wails 运行时并挂到 window.wails。
//
// 输入：无
// 输出：Promise；网页版没有运行时直接返回，不抛错
// 预期：在渲染前 await，App 的 subscribePush 才能拿到 Events.On
export async function initPushRuntime(): Promise<void> {
  // 用变量拼路径，避免 TS/Vite 在构建期解析这个仅运行期存在的模块
  const runtimePath = ['', 'wails', 'runtime.js'].join('/');
  try {
    const runtime = await import(/* @vite-ignore */ runtimePath);
    const w = window as unknown as { wails?: unknown };
    w.wails = w.wails ?? runtime;
  } catch {
    // 网页版没有可用运行时：忽略，subscribePush 走 SSE
  }
}

// subscribePush 订阅面板全部主题。
//
// 输入：handlers 每个主题的处理函数（缺省表示不关心）；
//       opts.onConnect 连接建立回调（仅网页版 SSE 有 ready）；opts.onError 断线回调
// 输出：取消订阅函数
// 预期：有桌面运行时用 Wails 事件，否则退回 /api/events；两条都是事件驱动，无轮询
export function subscribePush(
  handlers: Partial<Record<PushTopic, () => void>>,
  opts: { onConnect?: () => void; onError?: () => void } = {}
): () => void {
  const desktop = desktopEvents();
  if (desktop) {
    const offs = TOPICS.map((topic) => {
      const handler = handlers[topic];
      return handler ? desktop.On(desktopEvent(topic), handler) : () => {};
    });
    // 订阅建立即视为通道就绪：补拉一次，覆盖订阅前发生的挂载/配置推送
    opts.onConnect?.();
    return () => offs.forEach((off) => off());
  }

  const es = new EventSource('/api/events');
  if (opts.onError) es.onerror = opts.onError;
  if (opts.onConnect) es.addEventListener('ready', opts.onConnect);
  for (const topic of TOPICS) {
    const handler = handlers[topic];
    if (handler) es.addEventListener(topic, handler);
  }
  return () => es.close();
}
