// 桌面壳 e2e：键盘导航 + 假数据下的文件/设置/队列/同名冲突/下载操作。
//
// 输入：MYTGFS_E2E_EXE 指向 `go build -tags e2e` 的壳（Windows 建议加 -H=windowsgui）
//       MYTGFS_E2E_PORT 可选，默认 9339
// 输出：逐项 PASS/FAIL；任一失败退出码为 1
// 预期：不连 Telegram、不用真实账号；每条断言都等真实条件出现，
//       不用固定 sleep 猜时间，避免 CI 偶发失败
import { spawn, spawnSync } from 'node:child_process';
import { closeSync, existsSync, mkdirSync, mkdtempSync, openSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { setTimeout as sleep } from 'node:timers/promises';
import path from 'node:path';

const argTarget = (process.argv.find((a) => a.startsWith('--target=')) || '').split('=')[1];
const target = argTarget || process.env.MYTGFS_E2E_TARGET || 'shell';
if (target !== 'shell' && target !== 'web' && target !== 'android') {
  console.error('未知 --target：' + target + '（可选 shell / web / android）');
  process.exit(2);
}
// shell=真实 Wails 桌面壳（CDP 只有 Windows 的 WebView2 能开）
// web=网页版面板 + 系统自带 Chromium（三平台都能跑）
// android=模拟器/真机里的 APK，经 adb 转发 WebView 的 devtools 套接字
const isAndroid = target === 'android';
const androidAppID = process.env.MYTGFS_E2E_APP_ID || 'com.mytgfs.app';
// shell=真实 Wails 桌面壳（CDP 只有 Windows 的 WebView2 能开）
// web=网页版面板 + 系统自带 Chromium（三平台都能跑）
const exeDefault =
  target === 'web'
    ? process.platform === 'win32'
      ? 'build/mytgfs-e2e.exe'
      : 'build/mytgfs-e2e'
    : process.platform === 'win32'
      ? 'build/mytgfs-tray-e2e.exe'
      : 'build/mytgfs-tray-e2e';
// 可执行文件路径按仓库根算：npm 脚本的工作目录是 web/，相对路径会找错地方
const repoRoot = path.resolve(import.meta.dirname, '..', '..');
const exe = path.resolve(process.env.MYTGFS_E2E_EXE || path.join(repoRoot, exeDefault));
// 安卓目标不跑子进程，改为 adb 安装/启动 APK
const apkPath = path.resolve(
  process.env.MYTGFS_E2E_APK || path.join(repoRoot, 'build', 'mytgfs-e2e-android.apk')
);
const port = Number(process.env.MYTGFS_E2E_PORT || 9339);
const webPort = Number(process.env.MYTGFS_E2E_WEB_PORT || port + 1);
if (!isAndroid && !existsSync(exe)) {
  console.error('缺少可执行文件：' + exe + '（先 go build -tags e2e ./cmd/mytray 或 ./cmd/mytgfs）');
  process.exit(1);
}

// 临时配置：假 api_hash 只为通过启动校验，不连账号；下载目录也落在临时目录里
const workDir = mkdtempSync(path.join(tmpdir(), 'mytgfs-e2e-'));
mkdirSync(path.join(workDir, 'secrets'), { recursive: true });
const hashPath = path.join(workDir, 'secrets', 'api_hash');
writeFileSync(hashPath, '0123456789abcdef0123456789abcdef\n', 'utf8');
const cfgPath = path.join(workDir, 'config.toml');
const tomlString = (v) => v.replace(/\\/g, '\\\\');
writeFileSync(
  cfgPath,
  [
    '[telegram]',
    'api_id = 1',
    `api_hash_file = "${tomlString(hashPath)}"`,
    'bot_token_file = ""',
    '',
    '[mount]',
    'sync_seconds = 15',
    '',
    '[downloads]',
    `dir = "${tomlString(path.join(workDir, 'downloads'))}"`,
    '',
  ].join('\n'),
  'utf8'
);

// 子进程日志落到文件：CI 上起不来时能直接看到原因，而不是只剩一句「等不到调试目标」。
const logPath = path.join(workDir, 'shell.log');
// spawn 只接受已打开的 fd（WriteStream 在 fs.open 完成前 fd 为 null），
// 所以先用 openSync 拿到句柄，stdout/stderr 都指过去
const logFd = openSync(logPath, 'a');

// 子进程：shell 是桌面壳，web 是 `mytgfs web` 起的面板服务；
// 安卓没有子进程，进程由 adb 装好并拉起（见 bootAndroid）
const child = isAndroid
  ? null
  : target === 'web'
    ? spawn(exe, ['web', '--port', String(webPort)], {
        cwd: workDir,
        env: { ...process.env, MYTGFS_E2E_MOCK: '1' },
        stdio: ['ignore', logFd, logFd],
      })
    : spawn(exe, ['--config', cfgPath], {
        env: { ...process.env, MYTGFS_DEBUG_SHOW: '', MYTGFS_E2E_PORT: String(port), MYTGFS_E2E_MOCK: '1' },
        stdio: ['ignore', logFd, logFd],
      });

// 网页版需要另起一个浏览器；shell 目标没有这一层
let browser;

// chromiumCandidates 按平台列出可能的 Chromium 可执行文件。
//
// 输入：无
// 输出：候选路径/命令名数组（CHROME_PATH 优先）
// 预期：找不到任何一个时明确失败并打印尝试过的路径，不静默跳过用例
function chromiumCandidates() {
  const fromEnv = process.env.CHROME_PATH ? [process.env.CHROME_PATH] : [];
  if (process.platform === 'win32') {
    return [
      ...fromEnv,
      'C:/Program Files/Google/Chrome/Application/chrome.exe',
      'C:/Program Files (x86)/Google/Chrome/Application/chrome.exe',
      'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe',
      'C:/Program Files/Microsoft/Edge/Application/msedge.exe',
    ];
  }
  if (process.platform === 'darwin') {
    return [
      ...fromEnv,
      '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
      '/Applications/Chromium.app/Contents/MacOS/Chromium',
      '/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge',
    ];
  }
  return [...fromEnv, 'google-chrome', 'google-chrome-stable', 'chromium', 'chromium-browser', 'microsoft-edge'];
}

// killTree 结束一个子进程及其整棵子进程树。
//
// 输入：spawn 出来的子进程（可为空）
// 输出：无
// 预期：Windows 上 chrome 与 WebView2 都会派生多个子进程，只 kill 父进程会
//       留下仍占着调试端口的孤儿，下一轮 e2e 就会连到已经死掉的旧页面；
//       所以 Windows 走 taskkill /T 连子树一起收，其它平台用普通 kill
function killTree(proc) {
  if (!proc || !proc.pid) return;
  if (process.platform === 'win32') {
    try {
      spawnSync('taskkill', ['/PID', String(proc.pid), '/T', '/F'], { stdio: 'ignore' });
      return;
    } catch {
      /* taskkill 不可用时退回普通 kill */
    }
  }
  try {
    proc.kill();
  } catch {
    /* 已退出 */
  }
}

// shutdownBrowser 让浏览器自己退出，连子进程一起收。
//
// 输入：无（用全局调试端口）
// 输出：无
// 预期：只 kill 父进程在 Windows 上会留下占着调试端口的孤儿（chrome 的启动
//       进程会提前退出，把浏览器交给另一个进程）；CDP 的 Browser.close 让
//       chrome 自己正常关闭，进程树全清、端口一定释放；拿不到调试端点再退回 killTree
async function shutdownBrowser() {
  try {
    const info = await fetch('http://127.0.0.1:' + port + '/json/version').then((r) => r.json());
    if (info.webSocketDebuggerUrl) {
      const bw = new WebSocket(info.webSocketDebuggerUrl);
      await new Promise((resolve, reject) => {
        const timer = setTimeout(resolve, 3000);
        bw.addEventListener(
          'open',
          () => {
            clearTimeout(timer);
            resolve();
          },
          { once: true }
        );
        bw.addEventListener('error', reject, { once: true });
      });
      bw.send(JSON.stringify({ id: 1, method: 'Browser.close' }));
      await sleep(500);
      try {
        bw.close();
      } catch {
        /* 已经断开 */
      }
    }
  } catch {
    /* 浏览器已退出或调试端口不可用 */
  }
  killTree(browser);
}

// launchBrowser 起一个 headless 浏览器并等调试端口可用。
//
// 输入：url 要打开的页面
// 输出：浏览器子进程
// 预期：逐个候选尝试；命令不存在或端口起不来就换下一个，全失败才抛错
async function launchBrowser(url) {
  const tried = [];
  for (const bin of chromiumCandidates()) {
    const args = [
      '--headless=new',
      '--remote-debugging-port=' + port,
      '--user-data-dir=' + path.join(workDir, 'chrome'),
      '--no-first-run',
      '--no-default-browser-check',
      '--disable-gpu',
      '--disable-extensions',
      '--window-size=1120,760',
      '--force-device-scale-factor=1',
    ];
    if (process.platform === 'linux') args.push('--no-sandbox');
    args.push(url);
    const proc = spawn(bin, args, { stdio: ['ignore', logFd, logFd] });
    let spawnErr = '';
    proc.on('error', (err) => {
      spawnErr = err.message;
    });
    for (let i = 0; i < 100; i += 1) {
      if (spawnErr || proc.exitCode !== null) break;
      try {
        const res = await fetch('http://127.0.0.1:' + port + '/json/version');
        if (res.ok) return proc;
      } catch {
        /* 还没监听 */
      }
      await sleep(200);
    }
    tried.push(bin + (spawnErr ? '（' + spawnErr + '）' : '（端口未就绪）'));
    killTree(proc);
  }
  throw new Error('找不到可用的 Chrome/Chromium：' + tried.join('; '));
}

// waitPanel 等 `mytgfs web` 真的开始监听。
//
// 输入：无
// 输出：无（起不来就抛错并贴日志）
// 预期：先等面板再开浏览器，否则浏览器只会停在错误页不重试
async function waitPanel() {
  for (let i = 0; i < 300; i += 1) {
    if (child.exitCode !== null) {
      throw new Error('面板进程提前退出（code=' + child.exitCode + '）：' + shellLog());
    }
    try {
      const res = await fetch('http://127.0.0.1:' + webPort + '/api/state');
      if (res.ok) return;
    } catch {
      /* 还没监听 */
    }
    await sleep(200);
  }
  throw new Error('面板端口没起来：' + shellLog());
}

if (target === 'web') {
  try {
    await waitPanel();
    browser = await launchBrowser('http://127.0.0.1:' + webPort + '/');
  } catch (err) {
    console.error('ERROR ' + (err?.message || err));
    process.exit(1);
  }
}

// adbBin 定位 adb 可执行文件。
//
// 输入：无（读 ADB / ANDROID_HOME / ANDROID_SDK_ROOT 环境变量）
// 输出：可直接 spawn 的命令或绝对路径
// 预期：优先显式 ADB，其次 SDK 下的 platform-tools，最后交给 PATH
function adbBin() {
  if (process.env.ADB) return process.env.ADB;
  const sdk = process.env.ANDROID_HOME || process.env.ANDROID_SDK_ROOT;
  if (sdk) {
    const candidate = path.join(sdk, 'platform-tools', process.platform === 'win32' ? 'adb.exe' : 'adb');
    if (existsSync(candidate)) return candidate;
  }
  return 'adb';
}

// adb 同步执行一条 adb 命令。
//
// 输入：参数数组；timeout 毫秒
// 输出：spawnSync 结果（status/stdout/stderr），失败时把 stderr 拼进异常
// 预期：安装/启动这类长命令单独放宽超时，其它命令 60s 足够
function adb(args, timeout = 60000) {
  const res = spawnSync(adbBin(), args, { encoding: 'utf8', timeout, maxBuffer: 16 << 20 });
  if (res.error) throw new Error('adb ' + args.join(' ') + ' 调用失败：' + res.error.message);
  if (res.status !== 0) {
    throw new Error('adb ' + args.join(' ') + ' 退出码 ' + res.status + '：' + (res.stderr || res.stdout || '').trim());
  }
  return res;
}

// adbTry 与 adb 同，但不因非零退出码抛错。
//
// 输入：参数数组；timeout 毫秒
// 输出：spawnSync 结果
// 预期：给「失败是预期内状态」的探测用（例如进程还没起来时的 pidof）
function adbTry(args, timeout = 60000) {
  return spawnSync(adbBin(), args, { encoding: 'utf8', timeout, maxBuffer: 16 << 20 });
}

// bootAndroid 装 APK、拉起应用并把 WebView 的 devtools 套接字转发到本机端口。
//
// 输入：无（apkPath / androidAppID / port 来自模块级常量与环境变量）
// 输出：无；失败抛错
// 预期：条件轮询等设备与包管理器就绪，不用固定 sleep；
//       套接字名固定为 webview_devtools_remote_<pid>，转发后 CDP 与桌面壳完全一致
async function bootAndroid() {
  if (!existsSync(apkPath)) {
    throw new Error('缺少 APK：' + apkPath + '（先 build-android.ps1 -E2E）');
  }
  await waitFor('安卓设备上线', () => adb(['get-state']).stdout.trim() === 'device', 300000);
  await waitFor('设备启动完成', () => adb(['shell', 'getprop', 'sys.boot_completed']).stdout.trim() === '1', 300000);
  adb(['install', '-r', '-t', apkPath], 600000);
  // 清掉上一轮的数据：设置用例会改 config.toml，残留会让「默认值 15」的断言失败
  adb(['shell', 'pm', 'clear', androidAppID]);
  adb(['shell', 'am', 'force-stop', androidAppID]);
  // 启动组件名不能手写：清单里的类在 namespace 包下（com.wails.app），
  // applicationId 只决定安装包名，pkg/.Activity 会解析成不存在的类
  const resolved = adb(['shell', 'cmd', 'package', 'resolve-activity', '--brief', androidAppID])
    .stdout.split(/\r?\n/)
    .map((line) => line.trim())
    .filter((line) => line.includes('/'))
    .pop();
  if (!resolved) throw new Error('解析不到启动组件：' + androidAppID);
  // pm clear 会杀进程并删任务，紧跟着的 am start 可能被系统一起清掉；
  // 以「进程真的活着」为准，起不来就再发一次启动
  let pid = '';
  for (let attempt = 0; attempt < 20 && !pid; attempt += 1) {
    const started = spawnSync(adbBin(), ['shell', 'am', 'start', '-W', '-n', resolved], {
      encoding: 'utf8',
      timeout: 120000,
      maxBuffer: 16 << 20,
    });
    if (/Status: ok/.test(started.stdout || '')) {
      pid = adbTry(['shell', 'pidof', androidAppID]).stdout.trim();
    }
    if (!pid) await sleep(500);
  }
  if (!pid) throw new Error('应用进程起不来：' + androidAppID);
  adb(['forward', 'tcp:' + port, 'localabstract:webview_devtools_remote_' + pid.split(/\s+/)[0]]);
}

// configText 读面板实际写盘的配置文本。
//
// 输入：无
// 输出：config.toml 内容；读不到返回空串
// 预期：安卓的配置在应用沙盒 files/ 下，只能经 run-as 读取
function configText() {
  if (!isAndroid) {
    try {
      return readFileSync(cfgPath, 'utf8');
    } catch {
      return '';
    }
  }
  const res = spawnSync(adbBin(), ['shell', 'run-as', androidAppID, 'cat', 'files/config.toml'], {
    encoding: 'utf8',
    timeout: 60000,
  });
  return res.status === 0 ? res.stdout : '';
}

if (isAndroid) {
  try {
    await bootAndroid();
  } catch (err) {
    console.error('ERROR ' + (err?.message || err));
    process.exit(1);
  }
}

// shellLog 读子进程日志尾部，失败时贴进错误信息。
//
// 输入：最多取多少字符
// 输出：日志尾部字符串（读不到时返回空）
// 预期：只在失败路径调用，正常跑不产生额外 IO
function shellLog(max = 1200) {
  try {
    const text = readFileSync(logPath, 'utf8');
    return text.slice(-max);
  } catch {
    return '';
  }
}

let ws;
let nextId = 1;
const pending = new Map();
// 页面侧异常/console.error 收集起来，失败时一起打印
const pageErrors = [];

function rpc(method, params) {
  const id = nextId++;
  ws.send(JSON.stringify({ id, method, params }));
  return new Promise((resolve, reject) => {
    pending.set(id, { resolve, reject });
    setTimeout(() => {
      if (pending.delete(id)) reject(new Error(method + ' 超时'));
    }, 20000);
  });
}

async function evaluate(expression) {
  const res = await rpc('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
  if (res.exceptionDetails) throw new Error(res.exceptionDetails.exception?.description || '页面脚本异常');
  return res.result?.value;
}

// matchesTarget 判断一个调试目标是不是本次 e2e 自己的页面。
//
// 输入：CDP 的 /json/list 条目（含 url）
// 输出：true 表示属于本次运行
// 预期：web 只认面板端口、shell 只认 wails.localhost；不校验的话，上一轮
//       残留的同端口浏览器会被误连，报出一堆和本次无关的失败
function matchesTarget(t) {
  if (target === 'web') return t.url.startsWith('http://127.0.0.1:' + webPort);
  if (target === 'shell') return t.url.startsWith('http://wails.localhost');
  return true;
}

// findTarget 等调试目标出现。
//
// 输入：无
// 输出：可连的 page 目标
// 预期：CI 首次启动比本机慢得多，所以给足 180s（条件轮询，不是睡觉）；
//       进程提前退出就立刻失败并贴日志，不让它白等三分钟；
//       web 目标只认面板地址那一页，避免连到浏览器的空白页
async function findTarget() {
  for (let i = 0; i < 360; i += 1) {
    if (child && child.exitCode !== null) {
      throw new Error('面板进程提前退出（code=' + child.exitCode + '）：' + shellLog());
    }
    try {
      const list = await fetch('http://127.0.0.1:' + port + '/json/list').then((r) => r.json());
      const page = list.find(
        (t) => t.type === 'page' && t.webSocketDebuggerUrl && matchesTarget(t)
      );
      if (page) return page;
    } catch {
      /* 窗口还没起来 */
    }
    await sleep(500);
  }
  throw new Error('等不到调试目标：' + shellLog());
}

function check(label, ok, detail = '') {
  console.log((ok ? 'PASS ' : 'FAIL ') + label + (detail ? ' · ' + detail : ''));
  if (!ok) process.exitCode = 1;
}

// waitFor 轮询真实条件；条件达成立刻返回，超时抛错。固定等待只用于轮询间隔。
//
// 输入：说明；返回真值/假值的异步函数；超时毫秒
// 输出：首个真值
// 预期：不靠 sleep 猜时长，CI 慢机器也不会误判
async function waitFor(label, fn, timeout = 30000) {
  const started = Date.now();
  let last;
  while (Date.now() - started < timeout) {
    try {
      last = await fn();
      if (last) return last;
    } catch (err) {
      last = String(err);
    }
    await sleep(100);
  }
  throw new Error('等待超时：' + label + '（最后看到：' + JSON.stringify(last) + '，现场：' + JSON.stringify(await snapshotState()) + '）');
}

// snapshotState 采集失败现场，让 CI 红的时候日志自己说清卡在哪。
//
// 输入：无
// 输出：{active, rows, cursor, dialogs}；页面已崩时返回 null
// 预期：只在等待超时的错误路径调用，正常跑不产生额外往返
async function snapshotState() {
  try {
    return await evaluate(`(() => {
      const el = document.activeElement || document.body;
      const rows = [...document.querySelectorAll('[data-file-id]')];
      const label = el.getAttribute('aria-keyshortcuts')
        ? 'list'
        : el.getAttribute('aria-label') || el.id || '';
      return {
        active: el.tagName + ':' + label,
        rows: rows.length,
        cursor: rows.findIndex((r) => String(r.className).includes('ring-[#007AFF]')),
        cursorRow: rows.find((r) => String(r.className).includes('ring-[#007AFF]'))?.getAttribute('data-file-id') || '',
        dialogs: document.querySelectorAll('dialog[open]').length,
        toasts: [...document.querySelectorAll('[role="status"],[role="alert"]')]
          .map((e) => (e.textContent || '').trim())
          .filter(Boolean)
          .slice(-3),
      };
    })()`);
  } catch {
    return null;
  }
}

// pollTrue 与 waitFor 同，但超时返回 false 而不是抛错，用于「可能要多按一次」的键盘步骤。
async function pollTrue(fn, timeout = 2500) {
  const started = Date.now();
  while (Date.now() - started < timeout) {
    try {
      if (await fn()) return true;
    } catch {
      /* 页面重渲染中 */
    }
    await sleep(100);
  }
  return false;
}

// 真实按键：走 CDP 输入管线（keydown → 浏览器默认行为 → keyup），
// 页面里 preventDefault 会真的拦住文字输入，与用户手按一致。
//
// 输入：键名（ArrowUp/Down/Left/Right/Enter/Escape/Delete/F2/Tab/Space）
// 输出：无
// 预期：合成 KeyboardEvent 不会触发原生行为（不放文字、不改 select），
//       所以这里必须用 CDP dispatchKeyEvent
const VK = {
  ArrowUp: 38,
  ArrowDown: 40,
  ArrowLeft: 37,
  ArrowRight: 39,
  Enter: 13,
  Escape: 27,
  Delete: 46,
  F2: 113,
  Tab: 9,
  Space: 32,
};
const CODE = {
  ArrowUp: 'ArrowUp',
  ArrowDown: 'ArrowDown',
  ArrowLeft: 'ArrowLeft',
  ArrowRight: 'ArrowRight',
  Enter: 'Enter',
  Escape: 'Escape',
  Delete: 'Delete',
  F2: 'F2',
  Tab: 'Tab',
  Space: 'Space',
};

const key = async (k) => {
  const common = {
    key: k === 'Space' ? ' ' : k,
    code: CODE[k] ?? k,
    windowsVirtualKeyCode: VK[k] ?? 0,
    nativeVirtualKeyCode: VK[k] ?? 0,
  };
  await rpc('Input.dispatchKeyEvent', { type: 'keyDown', ...common });
  await rpc('Input.dispatchKeyEvent', { type: 'keyUp', ...common });
};

// rawKey 打一个可见字符：带 text 的 keyDown 才会真正插入；
// 页面若 preventDefault，文字不会落进输入框——正是「未按 Enter 不能编辑」要验的行为。
const rawKey = async (ch) => {
  const code = 'Key' + ch.toUpperCase();
  const vk = ch.toUpperCase().charCodeAt(0);
  await rpc('Input.dispatchKeyEvent', {
    type: 'keyDown',
    key: ch,
    code,
    text: ch,
    unmodifiedText: ch,
    windowsVirtualKeyCode: vk,
    nativeVirtualKeyCode: vk,
  });
  await rpc('Input.dispatchKeyEvent', { type: 'keyUp', key: ch, code });
};

// hasCursor 判断列表里是否已经有行带光标环（键盘游标已就位）。
const hasCursor = () =>
  evaluate(`(() => {
    const rows = [...document.querySelectorAll('[data-file-id]')];
    return rows.some((r) => String(r.className).includes('ring-[#007AFF]'));
  })()`);

// focusInfo 描述当前焦点落在哪一类控件上，供连续操作断言。
//
// 输入：无
// 输出：{kind, id, value?}；kind ∈ body|input|select|list|nav|other
// 预期：id 用 id/aria-label/href 这类稳定标识，避免文本随数据变化
const focusInfo = () =>
  evaluate(`(() => {
    const el = document.activeElement;
    if (!el || el === document.body) return { kind: 'body', id: 'body' };
    if (el.closest('nav[aria-label]')) {
      return { kind: 'nav', id: el.getAttribute('href') || 'nav' };
    }
    if (el.hasAttribute('aria-keyshortcuts')) return { kind: 'list', id: 'list' };
    if (el.tagName === 'INPUT') {
      return { kind: 'input', id: el.type + ':' + (el.getAttribute('aria-label') || el.id || ''), value: el.value };
    }
    if (el.tagName === 'SELECT') {
      return { kind: 'select', id: el.id || el.getAttribute('aria-label') || 'select', value: el.value };
    }
    return { kind: 'other', id: (el.getAttribute('aria-label') || el.textContent || '').replace(/\\s+/g, ' ').trim().slice(0, 24) };
  })()`);

// walkUntil 连续按键直到条件成立，返回经过的焦点轨迹。
//
// 输入：按键名；判定函数（接收 focusInfo）；最多按几次
// 输出：{ok, trail}
// 预期：轨迹用于失败时看清「卡在哪一项」，不猜次数
async function walkUntil(keyName, ok, max = 12) {
  const trail = [];
  for (let i = 0; i < max; i += 1) {
    await key(keyName);
    const info = await focusInfo();
    trail.push(info.id);
    if (ok(info)) return { ok: true, trail };
  }
  return { ok: false, trail };
}

// enterToolbar 从列表把焦点走到工具栏区域。
//
// 输入：无（要求当前焦点在列表内，内部会先确保列表聚焦）
// 输出：walkUntil 结果 {ok, trail}
// 预期：列表首项再按 ↑ 不拦截按键，焦点交给页面级移动，于是落到工具栏
async function enterToolbar() {
  await waitFor('列表聚焦', focusList);
  return walkUntil('ArrowUp', (i) => i.kind !== 'list', 14);
}

// walkToSearch 在工具栏里按候选方向找搜索框。
//
// 输入：方向数组（桌面只有 ArrowLeft；手机工具栏换行，搜索框在上方，先 ArrowUp）
// 输出：{ok, trail}
// 预期：每个方向都从工具栏起点重新来，避免上一次走错把焦点带到页面角落
async function walkToSearch(dirs) {
  let result = { ok: false, trail: [] };
  for (const dir of dirs) {
    const enter = await enterToolbar();
    if (!enter.ok) return enter;
    result = await walkUntil(dir, (i) => i.kind === 'input', 8);
    if (result.ok) return result;
  }
  return result;
}

// focused 返回当前焦点元素在 DOM 中的稳定标识：导航项用 href，其余退到 aria-label/文本。
//
// 输入：无
// 输出：标识字符串；焦点在 body 时为 BODY
// 预期：徽标数字会随数据刷新变化，所以不能用文本当身份
const focused = () =>
  evaluate(`(() => {
    const el = document.activeElement;
    if (!el || el === document.body) return 'BODY';
    const href = el.getAttribute('href');
    if (href) return href;
    const label = el.getAttribute('aria-label');
    if (label) return label;
    return (el.textContent || '').replace(/\\s+/g, ' ').trim().slice(0, 24);
  })()`);

const onNav = () =>
  evaluate(`(() => {
    const el = document.activeElement;
    return !!el && !!el.closest('nav[aria-label]');
  })()`);

const tabHash = () =>
  evaluate(`(() => {
    const link = [...document.querySelectorAll('nav[aria-label] a')].find(
      (a) => a.getAttribute('aria-current') === 'page'
    );
    return link ? link.getAttribute('href') : '';
  })()`);

const crumbText = () =>
  evaluate(`(() => {
    const nav = [...document.querySelectorAll('nav')].find((n) => n.textContent.includes('/'));
    return nav ? nav.textContent.replace(/\\s+/g, ' ').trim() : '';
  })()`);

const depth = (text) => text.split('/').filter(Boolean).length;

const focusList = () =>
  evaluate(`(() => {
    const list = document.querySelector('[aria-keyshortcuts]');
    if (!list) return false;
    if (document.activeElement !== list) list.focus();
    return document.activeElement === list;
  })()`);

// focusListStable 反复尝试直到焦点真的落在文件列表上。
//
// 输入：无
// 输出：无（拿不到焦点就抛错）
// 预期：弹窗关闭、列表重渲染都可能把焦点打回 body，
//       这里等的是「焦点确实在列表」这个条件，不是等固定时间
async function focusListStable() {
  for (let i = 0; i < 40; i += 1) {
    if (await focusList()) return;
    await sleep(50);
  }
  throw new Error('文件列表拿不到焦点');
}

// clearCursor 把列表里的键盘游标清空。
//
// 输入：无
// 输出：无（清不掉就抛错）
// 预期：Esc 先清勾选再清游标，所以可能按两次；
//       只有确实存在游标环时才按 Esc，否则 Esc 会退到上级目录
async function clearCursor() {
  await focusListStable();
  for (let i = 0; i < 3; i += 1) {
    if (!(await evaluate(`(() => {
      const rows = [...document.querySelectorAll('[data-file-id]')];
      return rows.some((r) => String(r.className).includes('ring-[#007AFF]'));
    })()`))) {
      return;
    }
    await key('Escape');
  }
  await waitFor('游标清空', () =>
    evaluate(`![...document.querySelectorAll('[data-file-id]')].some((r) => String(r.className).includes('ring-[#007AFF]'))`)
  );
}

// cursorRowId 读出当前游标所在行的 data-file-id。
//
// 输入：无
// 输出：条目 id 字符串；没有游标时返回空串
// 预期：删除前后要按 id 确认那一行真的从 DOM 消失，不能只看接口数据
const cursorRowId = () =>
  evaluate(`(() => {
    const row = [...document.querySelectorAll('[data-file-id]')].find((r) =>
      String(r.className).includes('ring-[#007AFF]')
    );
    return row ? row.getAttribute('data-file-id') || '' : '';
  })()`);

// selectLiveFirstRow 选中第一行，并确认这一行在服务端数据里仍然存在。
//
// 输入：无
// 输出：选中的条目 id
// 预期：删除/改名刚结束时 DOM 可能还停在旧列表，
//       只按 DOM 选中会把已经不存在的行带进后续操作，请求对空路径等于静默空转
async function selectLiveFirstRow() {
  for (let i = 0; i < 20; i += 1) {
    await selectFirstRow();
    const id = await cursorRowId();
    if (!id) continue;
    const files = await listFiles();
    if (files.some((f) => f.id === id)) return id;
  }
  throw new Error('选不到与服务端数据一致的行');
}

// selectFirstRow 从「没有游标」的状态选中第一行。
//
// 输入：无
// 输出：无（选不中就抛错）
// 预期：游标停在末项时再按 ↓ 会按设计把焦点送出列表（去分页按钮），
//       所以必须先把游标清掉，让 ↓ 从空状态落到第一行，不能沿用上一步的游标位置
async function selectFirstRow() {
  await clearCursor();
  await focusListStable();
  await key('ArrowDown');
  await waitFor('游标就位', hasCursor);
}

const setValue = (selector, value) =>
  evaluate(`(() => {
    const el = document.querySelector(${JSON.stringify(selector)});
    if (!el) return false;
    const proto = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
    Object.getOwnPropertyDescriptor(proto, 'value').set.call(el, ${JSON.stringify(value)});
    el.dispatchEvent(new Event('input', { bubbles: true }));
    return true;
  })()`);

const clickNav = (hash) =>
  evaluate(`(() => {
    const link = [...document.querySelectorAll('nav[aria-label] a')].find(
      (a) => a.getAttribute('href') === ${JSON.stringify(hash)}
    );
    if (!link) return false;
    link.click();
    return true;
  })()`);

// listFiles 取假后端整棵树；e2e 的断言都基于它的内容。
//
// 用相对路径：桌面壳的 origin 是 wails.localhost，网页版是 127.0.0.1，同一份断言两边都能跑
const listFiles = () =>
  evaluate(`fetch('/api/files?vol=demo&all=1').then((r) => r.json()).then((d) => d.files || [])`);

try {
  // 变量名不能叫 target：会遮蔽上面的 target（shell/web），下面 host 校验就判断错
  const page = await findTarget();
  ws = new WebSocket(page.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    ws.onopen = resolve;
    ws.onerror = () => reject(new Error('CDP 连接失败'));
  });
  ws.onmessage = (ev) => {
    const msg = JSON.parse(ev.data);
    if (msg.id && pending.has(msg.id)) {
      const { resolve, reject } = pending.get(msg.id);
      pending.delete(msg.id);
      msg.error ? reject(new Error(JSON.stringify(msg.error))) : resolve(msg.result);
      return;
    }
    // 页面里的异常必须出现在 CI 日志里：否则只剩「等不到弹窗」这种没信息量的失败
    if (msg.method === 'Runtime.exceptionThrown') {
      const d = msg.params.exceptionDetails;
      pageErrors.push(d.exception?.description || d.text || 'exception');
    }
    if (msg.method === 'Runtime.consoleAPICalled' && msg.params.type === 'error') {
      pageErrors.push(msg.params.args.map((a) => a.value ?? a.description ?? '').join(' '));
    }
  };
  await rpc('Runtime.enable');

  await waitFor('页面加载完成', async () => {
    const ready = await evaluate(`({ready: document.readyState === 'complete', host: location.host})`);
    return ready?.ready && ready.host === (target === 'web' ? '127.0.0.1:' + webPort : 'wails.localhost');
  });
  await waitFor('导航渲染', () => evaluate(`document.querySelectorAll('nav[aria-label] a').length > 0`));
  // 首屏数据到位再测键盘：否则徽标数字会在按键之间变化；
  // 用侧栏卷入口判断，避免依赖当前停在哪个页签
  await waitFor('首屏数据到位', () =>
    evaluate(`!!document.querySelector('[data-volume-id]')`)
  );

  // ---- 1. 方向键导航 ----
  // 桌面是左侧竖栏，上下键换项；安卓是底部横栏，只有左右键能换项
  const navNext = isAndroid ? 'ArrowRight' : 'ArrowDown';
  const navPrev = isAndroid ? 'ArrowLeft' : 'ArrowUp';
  await evaluate(`document.activeElement?.blur(); true`);
  await key(navNext);
  const first = await waitFor('方向键落到导航', async () => {
    const text = await focused();
    return text !== 'BODY' ? text : false;
  });
  check('无焦点时方向键落到导航', true, first);

  await key(navNext);
  const second = await waitFor('方向键换项', async () => {
    const text = await focused();
    return text !== first ? text : false;
  });
  check('方向键按位置移到下一项', true, first + ' → ' + second);

  await key(navPrev);
  const back = await waitFor('反向移回', async () => {
    const text = await focused();
    return text === first ? text : false;
  });
  check('反方向移回上一项', true, back);

  // ---- 2. 文件页：假卷、列表焦点、进出目录 ----
  await clickNav('#files');
  await waitFor('切到文件页', async () => (await tabHash()) === '#files');
  check('进入文件页', true, await tabHash());

  const state = await evaluate(`fetch('/api/state').then((r) => r.json())`);
  check(
    '假数据卷可用',
    state?.volumes?.length === 1 && state.volumes[0].name === 'demo' && state.status?.state_code === 'online',
    JSON.stringify(state?.volumes ?? null)
  );

  const initialFiles = await waitFor('假数据文件树', async () => {
    const files = await listFiles();
    return files.length >= 6 ? files : false;
  });
  check('假数据文件树完整', initialFiles.length >= 6, String(initialFiles.length));

  check('文件列表可聚焦', (await waitFor('文件列表出现', focusList)) === true);

  await key('ArrowDown');
  await key('Enter');
  const inFolder = await waitFor('进入子目录', async () => {
    const text = await crumbText();
    return depth(text) >= 2 ? text : false;
  });
  check('键盘进入子目录', true, inFolder);

  await key('Escape');
  const backRoot = await waitFor('返回上级目录', async () => {
    const text = await crumbText();
    return depth(text) < depth(inFolder) ? text : false;
  });
  check('键盘退回上级目录', true, backRoot);

  // ---- 3. 删除：确认框取消不删，确认后数据变少 ----
  await selectFirstRow();
  await key('Delete');
  await waitFor('删除确认框打开', () => evaluate(`!!document.querySelector('dialog[open]')`));
  check('Delete 打开删除确认框', true);

  const countAfterDialog = (await listFiles()).length;
  const idsBeforeDialog = new Set((await listFiles()).map((f) => f.id));
  // 用 Esc 取消：这就是用户真实的取消手势，别用 dialog.close()（脚本关窗不等 React 收尾）
  await key('Escape');
  await waitFor('删除确认框关闭', () => evaluate(`!document.querySelector('dialog[open]')`));
  const afterCancelIds = new Set((await listFiles()).map((f) => f.id));
  check(
    '取消删除不改数据',
    afterCancelIds.size === idsBeforeDialog.size &&
      [...idsBeforeDialog].every((id) => afterCancelIds.has(id))
  );

  await selectFirstRow();
  const doomedId = await cursorRowId();
  if (!doomedId) throw new Error('删除前拿不到游标行的 id');
  await key('Delete');
  await waitFor('删除确认框再次打开', () => evaluate(`!!document.querySelector('dialog[open]')`));
  const confirmed = await evaluate(`(() => {
    const btns = [...document.querySelectorAll('dialog[open] button')];
    const target = btns[btns.length - 1];
    if (!target) return false;
    target.click();
    return true;
  })()`);
  check('确认按钮可点击', confirmed === true);
  const afterDelete = await waitFor('删除后数据减少', async () => {
    const files = await listFiles();
    return !files.some((f) => f.id === doomedId) ? files : false;
  });
  check('确认删除后文件树变小', !afterDelete.some((f) => f.id === doomedId), String(afterDelete.length));
  await waitFor('删除确认框消失', () => evaluate(`!document.querySelector('dialog[open]')`));
  // 接口变了不等于界面变了：等被删那一行真的从 DOM 消失，否则下一步会选中陈旧行
  const doomedSelector = `[data-file-id="${doomedId}"]`;
  await waitFor('列表已刷新到删除后', () =>
    evaluate(`!document.querySelector(${JSON.stringify(doomedSelector)})`)
  );

  // ---- 4. 改名：F2 打开弹窗，提交后落到数据里 ----
  await waitFor('列表恢复', focusList);
  const renameTargetId = await selectLiveFirstRow();
  await key('F2');
  await waitFor('改名弹窗打开', () => evaluate(`!!document.querySelector('dialog[open] input')`));
  const renamed = await evaluate(`(() => {
    const input = document.querySelector('dialog[open] input');
    if (!input) return '';
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(input, 'e2e-renamed.txt');
    input.dispatchEvent(new Event('input', { bubbles: true }));
    return input.value;
  })()`);
  check('改名输入框可写', renamed === 'e2e-renamed.txt', renamed);
  await evaluate(`(() => {
    const form = document.querySelector('dialog[open] form');
    if (form) form.requestSubmit();
    return !!form;
  })()`);
  let renamedInTree;
  try {
    renamedInTree = await waitFor('改名落到数据里', async () => {
      const files = await listFiles();
      return files.some((f) => f.name === 'e2e-renamed.txt') ? 'e2e-renamed.txt' : false;
    });
  } catch (err) {
    // 超时要把改名目标与服务端现状一起打出来，否则只看到「false→false」无从判断
    const files = await listFiles();
    throw new Error(
      String(err) +
        ` | 改名目标=${renameTargetId} 服务端仍存在=${files.some((f) => f.id === renameTargetId)}`
    );
  }
  check('F2 改名生效', renamedInTree === 'e2e-renamed.txt', renamedInTree);

  // ---- 5. 连续键盘：列表 → 工具栏 → 搜索框 → 下拉 → 回到列表 ----
  await waitFor('搜索框出现', () => evaluate(`!!document.querySelector('input[type="search"]')`));
  await waitFor('列表聚焦', focusList);

  // 5.1 在列表里一路按 ↑，焦点必须能走出列表落到工具栏
  //     列表首项再按 ↑ 时不再拦截按键，交给页面级焦点移动
  const leaveList = await walkUntil('ArrowUp', (i) => i.kind !== 'list', 14);
  check('↑ 能把光标移出列表到工具栏', leaveList.ok, leaveList.trail.join(' > '));

  // 5.2 工具栏里连续按方向键，必须能走到搜索框（下拉框不再把方向键吃掉）
  //     桌面工具栏是一行，搜索框在最左边；手机上工具栏换行，搜索框在上方一行，
  //     所以先试 ↑，不行再退回 ←（每次都从列表重新走进工具栏）
  const searchDirs = isAndroid ? ['ArrowUp', 'ArrowLeft'] : ['ArrowLeft'];
  const toSearch = await walkToSearch(searchDirs);
  check('方向键从工具栏走到搜索框', toSearch.ok, toSearch.trail.join(' > '));

  // 5.2 未按 Enter 前，字符不许进搜索框（两段式：选中 ≠ 编辑）
  await rawKey('c');
  await rawKey('l');
  const beforeEnter = await focusInfo();
  check('搜索框未按 Enter 时不接收文字', beforeEnter.kind === 'input' && beforeEnter.value === '', JSON.stringify(beforeEnter));

  // 5.3 Enter 进入编辑，同一个键位立刻变成可输入
  await key('Enter');
  // 进入编辑的判据是 readOnly 翻掉：只等「已经能输入」这个状态，不等固定时间
  await waitFor('搜索框进入编辑', () =>
    evaluate(`(() => {
      const el = document.querySelector('input[type="search"]');
      return !!el && el.readOnly === false;
    })()`)
  );
  for (const ch of 'clip') await rawKey(ch);
  const filtered = await waitFor('搜索过滤生效', () =>
    evaluate(`(() => {
      const list = document.querySelector('[aria-keyshortcuts]');
      if (!list) return false;
      const text = list.textContent;
      return text.includes('clip.mp4') && !text.includes('song.mp3') && !text.includes('e2e-renamed');
    })()`)
  );
  check('Enter 后搜索框可输入并过滤列表', filtered === true);

  // 5.4 Esc 退出编辑、清空搜索并把焦点还给列表
  await key('Escape');
  const searchCleared = await waitFor('搜索清空且回到列表', async () => {
    const info = await focusInfo();
    const value = await evaluate(`document.querySelector('input[type="search"]').value`);
    return info.kind === 'list' && value === '' ? info.id : false;
  });
  check('Esc 退出搜索并回到列表', searchCleared === 'list');

  // 5.5 工具栏里连续按 →：每一步焦点都必须换到下一项，
  //     经过下拉框时左右键要能把焦点带出来（下拉只吃上下键）
  await walkUntil('ArrowUp', (i) => i.kind !== 'list', 14);
  const before = await focusInfo();
  const steps = [];
  let stuck = '';
  for (let i = 0; i < 10; i += 1) {
    const from = await focusInfo();
    await key('ArrowRight');
    const to = await focusInfo();
    if (to.id === from.id) {
      // 到了这一行最后一个控件，右边没有候选是正常结束；
      // 只有卡在下拉/输入框上走不掉才算回归
      if (from.kind === 'select' || from.kind === 'input') stuck = from.id + ' 卡住走不掉';
      break;
    }
    steps.push(from.id + '→' + to.id);
  }
  check('工具栏连续 → 不卡住（含下拉框）', stuck === '', stuck || steps.join(' | '));
  check('连续 → 至少走过三个控件', steps.length >= 3, String(steps.length));
  check('连续步进起点在工具栏', before.kind === 'input' || before.kind === 'select' || before.kind === 'other', JSON.stringify(before));

  // 5.6 连续反向走回搜索框，验证反向同样通（手机工具栏换行，方向同 5.2）
  const backToSearch = await walkToSearch(searchDirs);
  check('反方向能连续走回搜索框', backToSearch.ok, backToSearch.trail.join(' > '));

  // 5.7 回到列表确认键盘仍然可用：F2 能打开改名弹窗，Esc 能关掉
  // 先 Esc 清掉游标：游标停在末项时 ↓ 会按设计把焦点送出列表到分页按钮
  // 先等列表真的渲染出条目再按键：列表还在重渲染时按键会被当成页面级方向键
  await waitFor('列表有内容', () =>
    evaluate(`(() => {
      const list = document.querySelector('[aria-keyshortcuts]');
      return !!list && list.querySelectorAll('[data-file-id]').length > 0;
    })()`)
  );
  await selectFirstRow();
  await key('F2');
  const dialogBack = await waitFor('连续操作后 F2 仍能打开改名', () =>
    evaluate(`!!document.querySelector('dialog[open] input')`)
  );
  check('连续操作后文件页键盘仍可用', dialogBack === true);
  await key('Escape');
  await waitFor('改名弹窗关闭', () => evaluate(`!document.querySelector('dialog[open]')`));

  // ---- 6. 设置页：合法值写盘、非法值拒绝 ----
  await clickNav('#settings');
  await waitFor('切到设置页', async () => (await tabHash()) === '#settings');
  const fieldSel = '[id="setting-mount.sync_seconds"]';
  await waitFor('设置字段出现', () => evaluate(`!!document.querySelector(${JSON.stringify(fieldSel)})`));
  check('设置页显示默认值', (await evaluate(`document.querySelector(${JSON.stringify(fieldSel)}).value`)) === '15');

  await setValue(fieldSel, '17');
  const enabled = await waitFor('保存按钮可用', () =>
    evaluate(`(() => {
      const input = document.querySelector(${JSON.stringify(fieldSel)});
      const btns = [...(input?.closest('section')?.querySelectorAll('button') ?? [])];
      const btn = btns[btns.length - 1];
      return !!btn && !btn.disabled;
    })()`)
  );
  check('改值后保存按钮可用', enabled === true);
  await evaluate(`(() => {
    const input = document.querySelector(${JSON.stringify(fieldSel)});
    const btns = [...(input?.closest('section')?.querySelectorAll('button') ?? [])];
    btns[btns.length - 1]?.click();
    return true;
  })()`);
  const persisted = await waitFor('配置写盘', () => configText().includes('sync_seconds = 17'));
  check('设置写入配置文件', persisted !== false);
  const disabledAgain = await waitFor('保存后按钮回到禁用', () =>
    evaluate(`(() => {
      const input = document.querySelector(${JSON.stringify(fieldSel)});
      const btns = [...(input?.closest('section')?.querySelectorAll('button') ?? [])];
      const btn = btns[btns.length - 1];
      return !!btn && btn.disabled;
    })()`)
  );
  check('保存后不再标脏', disabledAgain === true);

  await setValue(fieldSel, '999999');
  await evaluate(`(() => {
    const input = document.querySelector(${JSON.stringify(fieldSel)});
    const btns = [...(input?.closest('section')?.querySelectorAll('button') ?? [])];
    btns[btns.length - 1]?.click();
    return true;
  })()`);
  const invalid = await waitFor('非法值被标红', () =>
    evaluate(`document.querySelector(${JSON.stringify(fieldSel)})?.getAttribute('aria-invalid') === 'true'`)
  );
  check('超范围值拒绝保存', invalid === true);
  check('非法值没有写盘', configText().includes('sync_seconds = 17'));

  // 改回合法值，避免影响后续步骤
  await setValue(fieldSel, '15');

  // 设置页 Esc 应该回到侧栏导航
  let settingsToNav = false;
  for (let i = 0; i < 3 && !settingsToNav; i += 1) {
    await key('Escape');
    settingsToNav = await pollTrue(onNav, 2000);
  }
  check('Esc 从设置页回到导航', settingsToNav === true);

  // ---- 7. 操作队列：假任务可取消并落「已取消」 ----
  await clickNav('#overview');
  await waitFor('切到概览页', async () => (await tabHash()) === '#overview');
  const queueCancel = await waitFor('操作队列渲染假任务', () =>
    evaluate(`!!document.querySelector('[data-testid="queue-cancel"]')`)
  );
  check('服务端队列任务给出取消按钮', queueCancel === true);

  const queueClicked = await evaluate(`(() => {
    const btn = document.querySelector('[data-testid="queue-cancel"]');
    if (!btn) return false;
    btn.click();
    return true;
  })()`);
  check('队列取消按钮可点击', queueClicked === true);

  const queueCancelled = await waitFor('队列任务落已取消', () =>
    evaluate(`!!document.querySelector('[data-queue-state="cancelled"]')`)
  );
  check('取消后任务显示已取消', queueCancelled === true);

  const queueCancelGone = await waitFor('已取消任务不再给取消按钮', () =>
    evaluate(`!document.querySelector('[data-testid="queue-cancel"]')`)
  );
  check('已取消任务不再给取消按钮', queueCancelGone === true);

  // ---- 8. 同名冲突：选一个根目录已存在的名字，确认前必须问怎么处理 ----
  if (!isAndroid) {
    await clickNav('#files');
    await waitFor('切回文件页', async () => (await tabHash()) === '#files');
    await waitFor('文件行渲染', () => evaluate(`document.querySelectorAll('[data-file-id]').length > 0`));

    // 用 DataTransfer 造一个与根目录 notes.txt 同名的文件，走真实的上传入口
    const staged = await evaluate(`(() => {
      const input = [...document.querySelectorAll('input[type=file]')].find((el) => !el.webkitdirectory);
      if (!input) return 'no-input';
      const dt = new DataTransfer();
      dt.items.add(new File(['hello'], 'notes.txt', { type: 'text/plain' }));
      input.files = dt.files;
      input.dispatchEvent(new Event('input', { bubbles: true }));
      input.dispatchEvent(new Event('change', { bubbles: true }));
      return 'ok';
    })()`);
    check('上传输入可注入文件', staged === 'ok', String(staged));

    const planOpen = await waitFor('上传清单打开', () =>
      evaluate(`(() => {
        const d = document.querySelector('dialog[open]');
        return !!d && (d.textContent || '').includes('准备上传');
      })()`)
    );
    check('选文件后先出上传清单', planOpen === true);

    const started = await evaluate(`(() => {
      const d = document.querySelector('dialog[open]');
      if (!d) return false;
      const btn = [...d.querySelectorAll('button')].find((b) => (b.textContent || '').trim() === '开始上传');
      if (!btn) return false;
      btn.click();
      return true;
    })()`);
    check('清单里点开始上传', started === true);

    const conflictOpen = await waitFor('同名冲突弹窗打开', () =>
      evaluate(`(() => {
        const d = document.querySelector('dialog[open]');
        return !!d && (d.textContent || '').includes('有同名文件');
      })()`)
    );
    check('同名文件弹出冲突选择', conflictOpen === true);

    const suggested = await evaluate(`(() => {
      const d = document.querySelector('dialog[open]');
      return !!d && (d.textContent || '').includes('notes (2).txt');
    })()`);
    check('给出不冲突的改名建议', suggested === true);

    const skipAll = await evaluate(`(() => {
      const d = document.querySelector('dialog[open]');
      if (!d) return false;
      const btn = [...d.querySelectorAll('button')].find((b) => (b.textContent || '').trim() === '全部跳过');
      if (!btn) return false;
      btn.click();
      return true;
    })()`);
    check('全部跳过可点', skipAll === true);

    const confirmDisabled = await waitFor('全部跳过时确认禁用', () =>
      evaluate(`(() => {
        const d = document.querySelector('dialog[open]');
        if (!d) return false;
        const btn = [...d.querySelectorAll('button')].find((b) => (b.textContent || '').trim() === '继续上传');
        return btn ? btn.disabled : false;
      })()`)
    );
    check('全部跳过时不允许确认', confirmDisabled === true);

    const conflictCancel = await evaluate(`(() => {
      const d = document.querySelector('dialog[open]');
      if (!d) return false;
      const btn = [...d.querySelectorAll('button')].find((b) => (b.textContent || '').trim() === '取消');
      if (!btn) return false;
      btn.click();
      return true;
    })()`);
    check('冲突弹窗可取消', conflictCancel === true);

    const planBack = await waitFor('取消冲突后回到清单', () =>
      evaluate(`(() => {
        const d = document.querySelector('dialog[open]');
        return !!d && (d.textContent || '').includes('准备上传');
      })()`)
    );
    check('取消冲突后回到上传清单', planBack === true);

    // 关掉清单，后面的下载页断言不受弹窗遮挡
    await evaluate(`(() => {
      const d = document.querySelector('dialog[open]');
      const btn = d && [...d.querySelectorAll('button')].find((b) => (b.textContent || '').trim() === '取消');
      if (btn) btn.click();
      return true;
    })()`);
    await waitFor('上传清单关闭', () => evaluate(`document.querySelectorAll('dialog[open]').length === 0`));
  }

  // ---- 9. 下载页：假文件渲染，Esc 逐层退回导航；安卓同样有下载页，只是没有「打开文件夹」 ----
  if (isAndroid) {
    // 侧栏在手机布局里仍留在 DOM（CSS 隐藏），所以按 href 断言而不是数个数
    const downloadsLinks = await evaluate(
      `document.querySelectorAll('nav[aria-label] a[href="#downloads"]').length`
    );
    check('安卓渲染下载页入口', downloadsLinks > 0, String(downloadsLinks));
    await clickNav('#files');
    await waitFor('切回文件页', async () => (await tabHash()) === '#files');
    await waitFor('文件行渲染', () => evaluate(`document.querySelectorAll('[data-file-id]').length > 0`));
    const hasDownloadBtn = await evaluate(`(() => {
      const btns = [...document.querySelectorAll('[data-file-id] button[aria-label]')];
      return btns.some((b) => /下载|Download/i.test(b.getAttribute('aria-label')));
    })()`);
    check('安卓渲染下载按钮', hasDownloadBtn === true);
    await clickNav('#downloads');
    await waitFor('切到下载页', async () => (await tabHash()) === '#downloads');
    const androidRows = await waitFor('安卓下载页列出本地文件', () =>
      evaluate(`(() => {
        const n = document.querySelectorAll('[data-download-name]').length;
        return n >= 2 ? String(n) : false;
      })()`)
    );
    check('安卓下载页列出本地文件', typeof androidRows === 'string', androidRows);
    // 安卓没有文件管理器语义：按钮改成导出到系统公共下载目录，不再是「打开文件夹」
    const dirAction = await evaluate(
      `document.querySelector('[data-testid="downloads-dir-action"]')?.getAttribute('data-dir-action') ?? null`
    );
    check('安卓不渲染打开下载文件夹', dirAction !== 'open', String(dirAction));
    check('安卓渲染导出到下载目录', dirAction === 'export');
  } else {
    await clickNav('#downloads');
    await waitFor('切到下载页', async () => (await tabHash()) === '#downloads');
    const rows = await waitFor('假下载文件渲染', () =>
      evaluate(`(() => {
        const n = document.querySelectorAll('[data-download-name]').length;
        return n >= 2 ? String(n) : false;
      })()`)
    );
    check('下载页列出本地文件', typeof rows === 'string', rows);

    let reachedNav = false;
    for (let i = 0; i < 4 && !reachedNav; i += 1) {
      await key('Escape');
      reachedNav = await pollTrue(onNav, 2000);
    }
    check('Esc 从下载页回到导航', reachedNav === true);
  }
} catch (err) {
  console.error('ERROR ' + (err?.message || err));
  if (pageErrors.length > 0) {
    console.error('页面异常：');
    for (const line of pageErrors.slice(0, 20)) console.error('  ' + line);
  }
  process.exitCode = 1;
} finally {
  try {
    ws?.close();
  } catch {
    /* 已断开 */
  }
  try {
    killTree(child);
  } catch {
    /* 已退出 */
  }
  if (isAndroid) {
    try {
      adb(['forward', '--remove', 'tcp:' + port]);
    } catch {
      /* 转发可能已被系统回收 */
    }
    try {
      adb(['shell', 'am', 'force-stop', androidAppID]);
    } catch {
      /* 应用可能已退出 */
    }
  }
  try {
    await shutdownBrowser();
  } catch {
    /* 已退出 */
  }
  try {
    closeSync(logFd);
  } catch {
    /* 已关闭 */
  }
  try {
    rmSync(workDir, { recursive: true, force: true });
  } catch {
    /* 目录被占用就留给系统清理 */
  }
}
