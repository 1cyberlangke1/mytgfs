// 前端推送约束检查：只有 src/events.ts 可以直接开 SSE 或订阅 Wails 事件。
//
// 输入：web/src 下的 .ts/.tsx/.js/.mjs/.cjs 文件
// 输出：违反即非零退出，CI 与 npm run lint 都会拦住
// 预期：组件只能 subscribePush；按「去掉注释后的整段文本」匹配，
//       这样跨行写法与别名（const ES = EventSource）也能拦住；
//       读 window.wails.platform() 探测平台不算推送通道，单独放行

import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';

import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('..', import.meta.url));
const srcDir = join(root, 'src');
const allowFile = join(srcDir, 'events.ts');

// 禁止在 events.ts 之外出现的写法：它们代表「自己另开一条推送通道」
const forbidden = [
  { pattern: /new\s+EventSource\s*\(/, why: '用 subscribePush，不要自己开 SSE' },
  { pattern: /\bEventSource\b/, why: '用 subscribePush，不要直接使用 EventSource' },
  { pattern: /\.wails\s*(?:\?\.)?\.\s*Events\b/, why: '用 subscribePush，不要直接订阅 Wails 事件' },
  { pattern: /\bEvents\s*\.\s*On\s*\(/, why: '用 subscribePush，不要直接订阅 Wails 事件' },
];

// stripComments 去掉行注释与块注释。
//
// 输入：源码文本
// 输出：保留代码与字符串的文本
// 预期：`http://` 里的 `//` 不被当成注释；注释掉的违规写法不应误报
function stripComments(text) {
  return text
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/(^|[^:])\/\/.*$/gm, '$1');
}

// walk 收集 src 下的脚本源码
function walk(dir) {
  const out = [];
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) {
      out.push(...walk(p));
    } else if (/\.(ts|tsx|js|mjs|cjs)$/.test(name)) {
      out.push(p);
    }
  }
  return out;
}

const violations = [];
for (const file of walk(srcDir)) {
  if (file === allowFile) continue;
  const code = stripComments(readFileSync(file, 'utf8'));
  for (const rule of forbidden) {
    if (rule.pattern.test(code)) {
      violations.push(`${relative(root, file)} ${rule.why}`);
    }
  }
}

if (violations.length > 0) {
  console.error('推送必须经由 src/events.ts：');
  for (const v of violations) console.error('  ' + v);
  process.exit(1);
}
console.log('推送约束检查通过：' + violations.length);
