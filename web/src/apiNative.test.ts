// @vitest-environment jsdom
//
// 安卓原生文件与目录导入（Java 桥）上下文回归测试。
//
// 输入：卷名与目录路径；
// 输出：{ uploaded, failed, failedNames }；
// 预期：必须保留 Java 桥对象的 this 上下文（receiver）。
//       如果以独立函数引用调用（如 const pick = bridge.pickFileImport; pick(...)），
//       Chromium V8 JavaBridge 会抛出：
//       "Java bridge method can't be invoked on a non-injected object"
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { importFileNative, importFolderNative, type MytgfsNativeBridge } from './api';

describe('安卓原生文件与目录选择器 (Java Bridge 上下文约束)', () => {
  let nativeBridge: MytgfsNativeBridge;

  beforeEach(() => {
    nativeBridge = {
      pickFileImport(this: MytgfsNativeBridge, _vol: string, _dir: string) {
        // 严格模拟 Android WebView @JavascriptInterface 的原生行为：
        // 方法必须由注入的原生对象作为 receiver (this) 调用；
        // 若引用被解构/脱离对象后裸调（this 丢失为 undefined 或 window），直接抛出 Chromium 标准异常。
        if (this !== nativeBridge) {
          throw new Error("Java bridge method can't be invoked on a non-injected object");
        }
        setTimeout(() => {
          const w = window as unknown as {
            _mytgfsFileImport?: (ok: number, failed: number, failedNames?: string[]) => void;
          };
          if (w._mytgfsFileImport) {
            w._mytgfsFileImport(1, 0, []);
          }
        }, 0);
      },
      pickFolderImport(this: MytgfsNativeBridge, _vol: string, _dir: string) {
        if (this !== nativeBridge) {
          throw new Error("Java bridge method can't be invoked on a non-injected object");
        }
        setTimeout(() => {
          const w = window as unknown as {
            _mytgfsFolderImport?: (ok: number, failed: number, failedNames?: string[]) => void;
          };
          if (w._mytgfsFolderImport) {
            w._mytgfsFolderImport(3, 0, []);
          }
        }, 0);
      },
    };

    (window as unknown as { mytgfsNative?: typeof nativeBridge }).mytgfsNative = nativeBridge;
  });

  afterEach(() => {
    const w = window as unknown as {
      mytgfsNative?: unknown;
      _mytgfsFileImport?: unknown;
      _mytgfsFolderImport?: unknown;
    };
    delete w.mytgfsNative;
    delete w._mytgfsFileImport;
    delete w._mytgfsFolderImport;
  });

  it('importFileNative 触发文件导入时保留原生对象的 this 上下文', async () => {
    // 输入：卷名 'v1'，目录 '/'
    // 输出：上传统计 { uploaded: 1, failed: 0, failedNames: [] }
    // 预期：正常完成导入，不得抛出 Java bridge method can't be invoked on a non-injected object
    const result = await importFileNative('v1', '/');
    expect(result).toEqual({ uploaded: 1, failed: 0, failedNames: [] });
  });

  it('importFolderNative 触发目录导入时保留原生对象的 this 上下文', async () => {
    // 输入：卷名 'v1'，目录 '/'
    // 输出：上传统计 { uploaded: 3, failed: 0, failedNames: [] }
    // 预期：正常完成导入，不得抛出 Java bridge method can't be invoked on a non-injected object
    const result = await importFolderNative('v1', '/');
    expect(result).toEqual({ uploaded: 3, failed: 0, failedNames: [] });
  });
});
