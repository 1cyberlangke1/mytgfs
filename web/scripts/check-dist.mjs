// dist 产物完整性：index.html 引用的每个 asset 都必须存在。
//
// 输入：dist/index.html（vite build 产物）
// 输出：引用齐全打印清单并退出 0；缺失列出来并退出 1
// 预期：Windows 与 Linux 构建的 asset hash 可能不同，
//       所以不做跨平台字节对比，只校验「提交的 dist 自身完整可加载」
import { readFileSync, existsSync } from 'node:fs';

const html = readFileSync('dist/index.html', 'utf8');
const refs = [...html.matchAll(/(?:src|href)="(\/assets\/[^"]+)"/g)].map((m) => m[1]);
if (refs.length === 0) {
  console.error('dist/index.html 没有引用任何构建产物');
  process.exit(1);
}
const missing = refs.filter((p) => !existsSync('dist' + p));
if (missing.length > 0) {
  console.error('dist 缺少被引用的产物：' + missing.join(', '));
  process.exit(1);
}
console.log('dist 产物完整：' + refs.join(', '));
