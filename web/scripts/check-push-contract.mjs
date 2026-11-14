// 前端推送约束检查：只有 src/events.ts 可以直接开 SSE 或读 Wails 运行时。
//
// 输入：web/src 下的 .ts/.tsx 文件
// 输出：违反即非零退出，CI 与 npm run lint 都会拦住
// 预期：组件只能 subscribePush，避免每个页面各写一套推送分支

import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';

import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('..', import.meta.url));
const srcDir = join(root, 'src');
const allowFile = join(srcDir, 'events.ts');

// 禁止在 events.ts 之外出现的写法：它们代表「自己另开一条推送通道」
const forbidden = [
  { pattern: /new\s+EventSource\s*\(/, why: '用 subscribePush，不要自己开 SSE' },
  { pattern: /\.wails\b/, why: '用 subscribePush，不要直接读 Wails 运行时' },
];

// walk 收集 src 下的 TypeScript 源码
function walk(dir) {
  const out = [];
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) {
      out.push(...walk(p));
    } else if (/\.(ts|tsx)$/.test(name)) {
      out.push(p);
    }
  }
  return out;
}

const violations = [];
for (const file of walk(srcDir)) {
  if (file === allowFile) continue;
  const text = readFileSync(file, 'utf8');
  const lines = text.split(/\r?\n/);
  lines.forEach((line, i) => {
    for (const rule of forbidden) {
      if (rule.pattern.test(line)) {
        violations.push(`${relative(root, file)}:${i + 1} ${rule.why}`);
      }
    }
  });
}

if (violations.length > 0) {
  console.error('推送必须经由 src/events.ts：');
  for (const v of violations) console.error('  ' + v);
  process.exit(1);
}
console.log('推送约束检查通过');
