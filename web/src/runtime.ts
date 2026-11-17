// Wails 运行期句柄：桌面端与安卓都由 /wails/runtime.js 提供。
//
// 输入：initPushRuntime 动态 import 到的模块
// 输出：wailsRuntime() 取句柄；未加载时返回 null
// 预期：安卓上 window.wails 是 Java 桥对象，没有 Call/Events 命名空间，
//       所以模块句柄单独保存，不能从 window.wails 上反查

export interface WailsRuntime {
  /** 按 FQN 调 Go 服务方法，例如 mytgfs/internal/web.Bridge.Do */
  Call?: { ByName(name: string, ...args: unknown[]): Promise<unknown> };
  /** 原生事件订阅，供 push 通道使用 */
  Events?: { On(name: string, cb: () => void): () => void };
}

let handle: WailsRuntime | null = null;

// setWailsRuntime 保存动态加载到的运行期模块。
export function setWailsRuntime(next: WailsRuntime): void {
  handle = next;
}

// wailsRuntime 返回已加载的运行期模块，网页版返回 null。
export function wailsRuntime(): WailsRuntime | null {
  return handle;
}
