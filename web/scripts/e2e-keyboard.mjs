// 桌面壳 e2e：键盘导航 + 假数据下的文件/设置/下载操作。
//
// 输入：MYTGFS_E2E_EXE 指向 `go build -tags e2e` 的壳（Windows 建议加 -H=windowsgui）
//       MYTGFS_E2E_PORT 可选，默认 9339
// 输出：逐项 PASS/FAIL；任一失败退出码为 1
// 预期：不连 Telegram、不用真实账号；每条断言都等真实条件出现，
//       不用固定 sleep 猜时间，避免 CI 偶发失败
import { spawn } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { setTimeout as sleep } from 'node:timers/promises';
import path from 'node:path';

const exe = path.resolve(
  process.env.MYTGFS_E2E_EXE ||
    (process.platform === 'win32' ? 'build/mytgfs-tray-e2e.exe' : 'build/mytgfs-tray-e2e')
);
const port = Number(process.env.MYTGFS_E2E_PORT || 9339);
if (!existsSync(exe)) {
  console.error('缺少可执行文件：' + exe + '（先 go build -tags e2e ./cmd/mytray）');
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

const child = spawn(exe, ['--config', cfgPath], {
  env: { ...process.env, MYTGFS_DEBUG_SHOW: '', MYTGFS_E2E_PORT: String(port), MYTGFS_E2E_MOCK: '1' },
  stdio: 'ignore',
});

let ws;
let nextId = 1;
const pending = new Map();

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

async function findTarget() {
  for (let i = 0; i < 120; i += 1) {
    try {
      const list = await fetch('http://127.0.0.1:' + port + '/json/list').then((r) => r.json());
      const page = list.find((t) => t.type === 'page' && t.webSocketDebuggerUrl);
      if (page) return page;
    } catch {
      /* 窗口还没起来 */
    }
    await sleep(500);
  }
  throw new Error('等不到 WebView2 调试目标');
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
async function waitFor(label, fn, timeout = 20000) {
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
  throw new Error('等待超时：' + label + '（最后看到：' + JSON.stringify(last) + '）');
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

const key = (k) =>
  evaluate(`(() => {
    const el = document.activeElement || document.body;
    el.dispatchEvent(new KeyboardEvent('keydown', { key: ${JSON.stringify(k)}, bubbles: true, cancelable: true }));
    return true;
  })()`);

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
    list.focus();
    return true;
  })()`);

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
const listFiles = () =>
  evaluate(`fetch('http://wails.localhost/api/files?vol=demo&all=1').then((r) => r.json()).then((d) => d.files || [])`);

try {
  const target = await findTarget();
  ws = new WebSocket(target.webSocketDebuggerUrl);
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
    }
  };
  await rpc('Runtime.enable');

  await waitFor('页面加载完成', async () => {
    const ready = await evaluate(`({ready: document.readyState === 'complete', host: location.host})`);
    return ready?.ready && ready.host === 'wails.localhost';
  });
  await waitFor('导航渲染', () => evaluate(`document.querySelectorAll('nav[aria-label] a').length > 0`));
  // 首屏数据到位再测键盘：否则徽标数字会在按键之间变化
  await waitFor('首屏数据到位', () =>
    evaluate(`(document.body.innerText || '').includes('演示卷')`)
  );

  // ---- 1. 方向键导航 ----
  await evaluate(`document.activeElement?.blur(); true`);
  await key('ArrowDown');
  const first = await waitFor('方向键落到导航', async () => {
    const text = await focused();
    return text !== 'BODY' ? text : false;
  });
  check('无焦点时方向键落到导航', true, first);

  await key('ArrowDown');
  const second = await waitFor('方向键换项', async () => {
    const text = await focused();
    return text !== first ? text : false;
  });
  check('方向键按位置移到下一项', true, first + ' → ' + second);

  await key('ArrowUp');
  const back = await waitFor('反向移回', async () => {
    const text = await focused();
    return text === first ? text : false;
  });
  check('反方向移回上一项', true, back);

  // ---- 2. 文件页：假卷、列表焦点、进出目录 ----
  await clickNav('#files');
  await waitFor('切到文件页', async () => (await tabHash()) === '#files');
  check('进入文件页', true, await tabHash());

  const state = await evaluate(`fetch('http://wails.localhost/api/state').then((r) => r.json())`);
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
  await focusList();
  await key('ArrowDown');
  await key('Delete');
  await waitFor('删除确认框打开', () => evaluate(`!!document.querySelector('dialog[open]')`));
  check('Delete 打开删除确认框', true);

  const countAfterDialog = (await listFiles()).length;
  await evaluate(`(() => {
    const dlg = document.querySelector('dialog[open]');
    if (dlg) dlg.close();
    return true;
  })()`);
  await waitFor('删除确认框关闭', () => evaluate(`!document.querySelector('dialog[open]')`));
  await sleep(200);
  check('取消删除不改数据', (await listFiles()).length === countAfterDialog);

  await focusList();
  await key('ArrowDown');
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
    return files.length < countAfterDialog ? files : false;
  });
  check('确认删除后文件树变小', afterDelete.length < countAfterDialog, String(afterDelete.length));
  await waitFor('删除确认框消失', () => evaluate(`!document.querySelector('dialog[open]')`));

  // ---- 4. 改名：F2 打开弹窗，提交后落到数据里 ----
  await waitFor('列表恢复', focusList);
  await key('ArrowDown');
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
  const renamedInTree = await waitFor('改名落到数据里', async () => {
    const files = await listFiles();
    return files.some((f) => f.name === 'e2e-renamed.txt') ? 'e2e-renamed.txt' : false;
  });
  check('F2 改名生效', renamedInTree === 'e2e-renamed.txt', renamedInTree);

  // ---- 5. 搜索：输入后只剩匹配项 ----
  await waitFor('搜索框出现', () => evaluate(`!!document.querySelector('input[type="search"]')`));
  await evaluate(`document.querySelector('input[type="search"]').focus(); true`);
  await setValue('input[type="search"]', 'clip');
  const filtered = await waitFor('搜索过滤生效', () =>
    evaluate(`(() => {
      const list = document.querySelector('[aria-keyshortcuts]');
      if (!list) return false;
      const text = list.textContent;
      return text.includes('clip.mp4') && !text.includes('song.mp3') && !text.includes('e2e-renamed');
    })()`)
  );
  check('搜索框过滤列表', filtered === true);
  await setValue('input[type="search"]', '');
  await waitFor('清空搜索恢复列表', () =>
    evaluate(`(() => {
      const list = document.querySelector('[aria-keyshortcuts]');
      return !!list && list.textContent.includes('clip.mp4');
    })()`)
  );

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
  const persisted = await waitFor('配置写盘', () => {
    try {
      return readFileSync(cfgPath, 'utf8').includes('sync_seconds = 17');
    } catch {
      return false;
    }
  });
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
  check('非法值没有写盘', readFileSync(cfgPath, 'utf8').includes('sync_seconds = 17'));

  // 改回合法值，避免影响后续步骤
  await setValue(fieldSel, '15');

  // 设置页 Esc 应该回到侧栏导航
  let settingsToNav = false;
  for (let i = 0; i < 3 && !settingsToNav; i += 1) {
    await key('Escape');
    settingsToNav = await pollTrue(onNav, 2000);
  }
  check('Esc 从设置页回到导航', settingsToNav === true);

  // ---- 7. 下载页：假文件渲染，Esc 逐层退回导航 ----
  await clickNav('#downloads');
  await waitFor('切到下载页', async () => (await tabHash()) === '#downloads');
  const rows = await waitFor('假下载文件渲染', () =>
    evaluate(`(() => {
      const text = document.querySelector('main')?.textContent ?? '';
      return text.includes('已下载报告.pdf') && text.includes('已下载歌曲.mp3') ? text : false;
    })()`)
  );
  check('下载页列出本地文件', typeof rows === 'string');

  let reachedNav = false;
  for (let i = 0; i < 4 && !reachedNav; i += 1) {
    await key('Escape');
    reachedNav = await pollTrue(onNav, 2000);
  }
  check('Esc 从下载页回到导航', reachedNav === true);
} catch (err) {
  console.error('ERROR ' + (err?.message || err));
  process.exitCode = 1;
} finally {
  try {
    ws?.close();
  } catch {
    /* 已断开 */
  }
  try {
    child.kill();
  } catch {
    /* 已退出 */
  }
  try {
    rmSync(workDir, { recursive: true, force: true });
  } catch {
    /* 目录被占用就留给系统清理 */
  }
}
