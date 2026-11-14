// 桌面端端到端测试（只读）：驱动真实 Wails 窗口里的 WebView2。
//
// 输入：build/mytgfs-tray-e2e.exe（`go build -tags e2e`）
// 输出：控制台逐项 PASS/FAIL；任一失败退出码 1
// 预期：
//   - 用 WebView2 的远程调试端口连进真实窗口，而不是另起一个 http 服务
//   - 只发 GET 请求，对 Telegram 账号与频道零写入：不建卷、不上传、不删除
//   - 进程由本脚本启动并在结束时关闭，不碰用户已有的托盘实例
import { spawn } from 'node:child_process';
import { existsSync } from 'node:fs';
import { setTimeout as sleep } from 'node:timers/promises';
import path from 'node:path';

const exe = path.resolve(process.env.MYTGFS_E2E_EXE || 'build/mytgfs-tray-e2e.exe');
const port = Number(process.env.MYTGFS_E2E_PORT || 9333);
const volume = process.env.MYTGFS_E2E_VOLUME || 'demo';

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
      msg.error ? reject(new Error(JSON.stringify(msg.error))) : resolve(msg.result);
    }
  };
  await rpc('Runtime.enable');

  // 等页面导航到 wails.localhost 并加载完成：
  // 导航期间执行上下文会被销毁，这里在 Node 侧静默重试
  for (let i = 0; i < 120; i++) {
    try {
      const ready = await evaluate(
        `({ ready: document.readyState === 'complete', host: location.host })`
      );
      if (ready && ready.ready && ready.host === 'wails.localhost') break;
    } catch {}
    await sleep(500);
  }

  // 1. 等窗口连上 Telegram（connecting 属于正常过程；账号没有频道时卷数为 0 也正常）
  //    页面尚未导航完成时 fetch 会抛错：静默重试，不算失败
  const state = await evaluate(`(async () => {
    for (let i = 0; i < 240; i++) {
      try {
        const s = await fetch('http://wails.localhost/api/state').then(r => r.json());
        if (s.status && s.status.state_code !== 'connecting') return s;
      } catch {}
      await new Promise(r => setTimeout(r, 500));
    }
    return null;
  })()`);
  check('/api/state 可读', !!state, `status=${state?.status?.state_code ?? '超时'}`);
  check(
    '卷列表可读（0 个也正常：账号可能没有频道）',
    Array.isArray(state?.volumes),
    `${state?.volumes?.length ?? 0} 个`
  );

  // 2. 文件列表可读（只 GET，不写频道；没有卷或卷为空都正常）
  const files = await evaluate(`fetch('http://wails.localhost/api/files?vol=' + encodeURIComponent(${JSON.stringify(volume)}) + '&all=1')
    .then(r => r.json()).catch(() => null)`);
  check(
    `/api/files 可读（${volume}，卷不存在也正常）`,
    !!files && typeof files === 'object',
    `${JSON.stringify(files).slice(0, 80)}`
  );

  // 3. 缩略图接口可用：有图返回 JPEG，无图 404，都算只读正常
  const img = (files?.files || []).find(
    (f) => (f.category === 'image' || f.category === 'video') && f.path
  );
  if (img) {
    const url = img.category === 'image'
      ? '/api/thumb?' + new URLSearchParams({ vol: volume, path: img.path })
      : '/api/vthumb?' + new URLSearchParams({ vol: volume, path: img.path, v: img.updatedAt });
    const thumb = await evaluate(`(async () => {
      const r = await fetch(${JSON.stringify(url)});
      return { status: r.status, type: r.headers.get('content-type') };
    })()`);
    check(
      '缩略图接口（只读）',
      thumb?.status === 200 || thumb?.status === 404,
      `${thumb?.status} ${thumb?.type}`
    );
  }

  // 4. 下载目录接口可读
  const downloads = await evaluate(`fetch('http://wails.localhost/api/downloads').then(r => r.json())`);
  check(
    '/api/downloads 可读',
    !!downloads && typeof downloads.dir === 'string',
    `dir=${downloads?.dir ?? '?'}`
  );

  console.log('只读端到端结束：未向 Telegram 写入任何数据');
} catch (e) {
  console.error('E2E_ERROR', e.message);
  process.exitCode = 1;
} finally {
  if (ws) ws.close();
  child.kill();
}
