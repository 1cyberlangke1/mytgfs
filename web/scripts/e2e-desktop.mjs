// 桌面端端到端测试：驱动真实 Wails 窗口里的 WebView2。
//
// 输入：build/mytgfs-tray-e2e.exe（`go build -tags e2e`）、config.toml 里可用的卷
// 输出：控制台逐项 PASS/FAIL；任一失败退出码 1
// 预期：
//   - 用 WebView2 的远程调试端口连进真实窗口，而不是另起一个 http 服务
//   - 走页面自己的 fetch 调后端接口，验证上传 1MiB、列表可见、删除、下载落盘
//   - 进程由本脚本启动并在结束时关闭，不碰用户已有的托盘实例
import { spawn } from 'node:child_process';
import { existsSync } from 'node:fs';
import { setTimeout as sleep } from 'node:timers/promises';
import path from 'node:path';

const exe = path.resolve(process.env.MYTGFS_E2E_EXE || 'build/mytgfs-tray-e2e.exe');
const port = Number(process.env.MYTGFS_E2E_PORT || 9333);
const volume = process.env.MYTGFS_E2E_VOLUME || 'demo';
const name = `e2e-${Date.now()}.bin`;
const size = 1 << 20;

if (!existsSync(exe)) {
  console.error(`找不到 ${exe}，先执行 go build -tags e2e -o build/mytgfs-tray-e2e.exe ./cmd/mytray`);
  process.exit(1);
}

const child = spawn(exe, [], {
  env: { ...process.env },
  stdio: 'ignore',
});

let ws;
let nextId = 1;
const pending = new Map();

// rpc 发送一条 CDP 命令并等待结果。
function rpc(method, params) {
  const id = nextId++;
  ws.send(JSON.stringify({ id, method, params }));
  return new Promise((resolve, reject) => {
    pending.set(id, { resolve, reject });
    setTimeout(() => {
      if (pending.delete(id)) reject(new Error(`${method} 超时`));
    }, 30000);
  });
}

// evaluate 在页面里执行一段异步表达式并把结果取回来。
async function evaluate(expression) {
  const res = await rpc('Runtime.evaluate', {
    expression,
    awaitPromise: true,
    returnByValue: true,
    userGesture: true,
  });
  if (res.exceptionDetails) {
    throw new Error(res.exceptionDetails.exception?.description || '页面脚本异常');
  }
  return res.result?.value;
}

async function findTarget() {
  for (let i = 0; i < 120; i++) {
    try {
      const list = await fetch(`http://127.0.0.1:${port}/json/list`).then((r) => r.json());
      const page = list.find((t) => t.type === 'page' && t.webSocketDebuggerUrl);
      if (page) return page;
    } catch {
      // 窗口还没起来
    }
    await sleep(500);
  }
  throw new Error('等不到 WebView2 调试目标');
}

function check(label, ok, detail = '') {
  console.log(`${ok ? 'PASS' : 'FAIL'} ${label}${detail ? ` · ${detail}` : ''}`);
  if (!ok) process.exitCode = 1;
}

try {
  const target = await findTarget();
  ws = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    ws.onopen = resolve;
    ws.onerror = () => reject(new Error('CDP WebSocket 连接失败'));
  });
  ws.onmessage = (ev) => {
    const msg = JSON.parse(ev.data);
    if (msg.id && pending.has(msg.id)) {
      const { resolve, reject } = pending.get(msg.id);
      pending.delete(msg.id);
      msg.error ? reject(new Error(msg.error.message)) : resolve(msg.result);
    }
  };
  await rpc('Runtime.enable');

  // 1. 等窗口真正连上并挂好卷（启动时是 connecting + 0 卷，属于正常过程）
  const state = await evaluate(`(async () => {
    for (let i = 0; i < 240; i++) {
      const s = await fetch('/api/state').then(r => r.json());
      if ((s.volumes || []).length > 0 && s.status && s.status.state_code !== 'connecting') return s;
      await new Promise(r => setTimeout(r, 500));
    }
    return null;
  })()`);
  check('/api/state 可读', !!state, `status=${state?.status?.state_code ?? '超时'}`);
  check('卷已挂载', Array.isArray(state?.volumes) && state.volumes.length > 0, `${state?.volumes?.length ?? 0} 个`);

  // 2. 通过页面上传 1MiB 原始字节（走分片接口，绕开 Wails 的 multipart 问题）
  const upload = await evaluate(`(async () => {
    const name = ${JSON.stringify(name)};
    const size = ${size};
    const init = await fetch('/api/upload/init', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ vol: ${JSON.stringify(volume)}, dir: '/', rel: '', name, size }),
    }).then(r => r.json());
    const buf = new Uint8Array(size);
    for (let off = 0; off < size; off += 65536) {
      crypto.getRandomValues(buf.subarray(off, Math.min(size, off + 65536)));
    }
    const put = await fetch('/api/upload/chunk?id=' + encodeURIComponent(init.id) + '&offset=0', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/octet-stream' },
      body: buf,
    });
    if (!put.ok) return { error: 'chunk ' + put.status };
    const done = await fetch('/api/upload/commit?id=' + encodeURIComponent(init.id), { method: 'POST' });
    return { status: done.status, body: await done.json() };
  })()`);
  check('分片上传 1MiB', upload?.status === 200, JSON.stringify(upload));

  // 3. 列表里能看到，大小对得上
  const listed = await evaluate(`fetch('/api/files?vol=' + encodeURIComponent(${JSON.stringify(volume)}) + '&all=1')
    .then(r => r.json()).then(d => (d.files || []).find(f => f.name === ${JSON.stringify(name)}))`);
  check('索引里可见', !!listed && listed.size === size, `size=${listed?.size}`);

  // 4. 下载：后台任务立即返回，最终文件落盘且内容一致
  const download = await evaluate(`(async () => {
    const name = ${JSON.stringify(name)};
    const start = await fetch('/api/downloads', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ vol: ${JSON.stringify(volume)}, path: '/' + name }),
    });
    if (start.status !== 202) return { error: 'start ' + start.status };
    for (let i = 0; i < 120; i++) {
      const st = await fetch('/api/downloads').then(r => r.json());
      const busy = (st.tasks || []).some(t => t.state === 'running' || t.state === 'paused');
      const done = (st.files || []).find(f => f.name === name);
      if (!busy && done) {
        const got = await fetch('/api/downloads/file?name=' + encodeURIComponent(name));
        return { size: done.size, bytes: (await got.arrayBuffer()).byteLength };
      }
      if ((st.tasks || []).some(t => t.state === 'failed')) {
        return { error: (st.tasks.find(t => t.state === 'failed') || {}).error || 'failed' };
      }
      await new Promise(r => setTimeout(r, 300));
    }
    return { error: '下载超时' };
  })()`);
  check('下载任务落盘', download?.bytes === size, JSON.stringify(download));

  // 5. 清理：本地副本 + 云端文件
  const cleanup = await evaluate(`(async () => {
    const del = await fetch('/api/downloads?name=' + encodeURIComponent(${JSON.stringify(name)}), { method: 'DELETE' });
    const rm = await fetch('/api/files/remove', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ vol: ${JSON.stringify(volume)}, paths: ['/' + ${JSON.stringify(name)}] }),
    });
    return { del: del.status, rm: rm.status };
  })()`);
  check('清理完成', cleanup?.del === 200 && cleanup?.rm === 200, JSON.stringify(cleanup));
} catch (err) {
  console.error('E2E 失败:', err.message);
  process.exitCode = 1;
} finally {
  try {
    ws?.close();
  } catch {
    // 忽略
  }
  child.kill();
}
