# mytgfs

[![CI](https://github.com/1cyberlangke1/mytgfs/actions/workflows/ci.yml/badge.svg)](https://github.com/1cyberlangke1/mytgfs/actions/workflows/ci.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/1cyberlangke1/mytgfs)](https://github.com/1cyberlangke1/mytgfs/blob/master/go.mod)
[![License](https://img.shields.io/github/license/1cyberlangke1/mytgfs)](https://github.com/1cyberlangke1/mytgfs/blob/master/LICENSE)
[![Release](https://img.shields.io/github/v/release/1cyberlangke1/mytgfs)](https://github.com/1cyberlangke1/mytgfs/releases)

把 Telegram 私有频道当成自己的网盘：文件存在你控制
的频道里，本地不重复占空间；桌面托盘常驻 + Web 面板操作，
像普通网盘一样上传、预览、搜索、下载。

- 文件正文存频道，本地 SQLite 只是索引缓存，删掉能自动重建
- 改名/移动/删除只编辑节点自己那一条消息，与目录里有多少文件无关
- 断电、断网不丢任务：写操作先进持久化队列，重启自动续传
- 同内容文件秒传：按大小 + sha256 复用已上传的字节
- 面板支持拖拽/整目录上传、在线播放、下载到本地目录

> 频道不是专属文件仓库，里面可能有聊天消息。程序**只处理自己格式的消息**
> （正文含 `v`/`t` 字段的 JSON），不会扫描、删除频道里任何其它消息。

## 快速开始

### 安装

- **发行包**：从 [Releases](https://github.com/1cyberlangke1/mytgfs/releases) 下载对应平台的包，
  解压后在 PowerShell 里执行一次 `.\install.ps1`（装到 `%LOCALAPPDATA%\mytgfs`，创建开始菜单快捷方式）。
- **从源码构建**：Windows 执行 `.\build.ps1`（产出 `build\mytgfs.exe` 与 `build\mytgfs-tray.exe`，
  `-Package` 附带分发 zip）；其它平台 `go build -o mytgfs ./cmd/mytgfs`。

`mytgfs-tray.exe` 是日常入口（GUI 子系统，全程无黑框）；`mytgfs.exe` 是命令行版，适合脚本与无界面环境。

### 首次配置（面板，推荐）

1. 从开始菜单打开 **mytgfs**（托盘），右键托盘图标打开面板。
2. 在「设置」页填入 `api_id` / `api_hash` / bot token 并保存，登录 bot。
3. 登录 user 会话（手机号 + 验证码）：建频道、回填历史只有账号能做。
4. 在「文件」页建一个卷：程序会建私有频道、把 bot 拉进去提权、关通知、设频道头像。
5. 上传文件。下载的文件落在配置的下载目录（默认 `data/downloads`）。

凭证（`api_id` / `api_hash` / bot token）也可以之后在面板里补填，不必先改任何文件。

### 命令行方式（可选）

命令行不做面板做不到的事，只是同一套能力的脚本化入口。首次使用仍需准备：

1. `Copy-Item config.example.toml config.toml`，把 `api_id` 填进 `config.toml`；
   `api_hash` 写进 `secrets/api_hash`、bot token 写进 `secrets/bot_token`（各单行纯文本）。
   这三个路径也可在面板设置页填写，等价。
2. `mytgfs auth bot`、`mytgfs auth user`（手机号登录）。
3. `mytgfs volume create --name demo` 建卷，之后 `mytgfs web` 打开面板或直接用 `put` / `get` 等子命令。

`config.toml`、`secrets/`、`data/` 都已被 `.gitignore` 忽略，不会提交。

## 常用命令

| 命令 | 作用 |
|---|---|
| `mytgfs auth bot` / `auth user` | bot token / 手机号登录 |
| `mytgfs volume create\|delete --name <n>` | 建卷（建频道+拉 bot+提权+设头像）/ 删卷 |
| `mytgfs volume check` | 检查每个卷的频道是否仍可读写 |
| `mytgfs web [--port N]` | 启动本地 Web 面板（默认端口取 `[web].port`） |
| `mytgfs tray` | 系统托盘常驻（自动同步、按需面板） |
| `mytgfs ls` / `mkdir` / `rm` / `mv` | 列目录 / 建目录 / 删除 / 移动改名 |
| `mytgfs get [--name 卷] <远端> [本地\|-]` | 下载（`-` 输出到 stdout） |
| `mytgfs put [--name 卷] [--remote /目录] <路径...>` | 批量上传文件/目录 |
| `mytgfs sync [--name 卷] [--rebuild]` | 增量同步；`--rebuild` 全量重建索引 |
| `mytgfs check [--name 卷] [--fix]` | 以频道全量节点为准核对索引 |
| `mytgfs config show\|get\|set\|keys` | 读写配置（与面板设置页等价） |
| `mytgfs startup enable\|disable\|status` | 开机自启（默认关） |

各子命令都支持 `--help` 看参数；大部分支持 `--json` 输出结构化结果。
`MYTGFS_LANG` 可强制界面语言（`zh-CN` / `en`）。

## 配置

见 `config.example.toml`，要点：

- `[telegram]`：`api_id` 填这里；`api_hash_file` / `bot_token_file` 指向 `secrets/` 下的文件。
- `[mount]`：`read_chunk_mb`（读分块，默认 8）、`download_parallel`（下载连接数，默认 4）、
  `upload_parallel`（上传并发分片，默认 4）、`auto_sync`（自动同步开关，缺省开启）、
  `sync_seconds`（自动同步间隔，默认 15 秒，负数关闭）。
- `[web]`：`port`（面板端口，默认 8720，被占用时自动退回随机端口）。
- `[downloads]`：`dir` 是面板「下载」页的落盘目录，默认 `data/downloads`；
  相对路径按配置文件所在目录解析，绝对路径原样使用。
- `[log]`：slog + 轮转日志，默认写到 `data/mytgfs.log`。

## 数据放在哪

| 内容 | 位置 | 说明 |
|---|---|---|
| 文件正文 + 元数据 | Telegram 频道 | 每个文件/目录一条消息；频道即权威数据 |
| 本地索引 | `data/index-<卷名>.db` | SQLite；**可丢弃**，删掉后启动时自动翻频道历史重建 |
| 上传断点 | `data/uploads/<sha256>.json` | 大文件分片进度，中断后按内容摘要续传 |
| 待补传记账 | 索引库 `pending` 表 | 见下节「可靠性」 |
| 会话 / 密钥 | `secrets/` | bot/user 会话与加密密钥，**不能删** |
| 下载目录 | `data/downloads` | 可用 `[downloads].dir` 改到别的磁盘 |

## 可靠性

- **写不丢**：关闭写入时先落临时文件、在 `pending` 表记一笔，上传成功才清账删临时文件。
  中途崩溃/断网/杀进程，下次启动自动补传；临时文件损坏或对不上账就只清账不上传，不会把坏内容写进频道。
- **分片断点续传**：大文件传一半中断，重启后跳过已完成分片继续发。
- **上传成功、索引没写成**也会记账，下次只补登记，不重传字节、不产生重复消息。
- **秒传**：提交前按大小 + sha256 找同内容，命中就发一条引用已有 document 的消息，零重传；
  复用失败自动回退正常上传。
- **持久化队列**：上传/建目录/改名/删除/复制先落 SQLite 再执行，断电重启自动重排；
  同卷串行、跨卷并行；`FLOOD_WAIT` / `429` 按服务端要求的秒数等待后重试，确定性错误立即失败并显示原因。
  排队上限 2000，已结束记录保留最近 20 条，面板「清空记录」真删。
- **改名/移动先改远端再改本地**，本地写入失败按远端对账，不会出现两边名字分叉。
- **删除是 O(1) 打标记**：只把被删节点那条消息改写成删除标记，子树消息不动。
  bot 会话有「消息创建超过 48 小时不能删」的平台限制，打标记走消息编辑，不受影响。
- **引用过期自动刷新**：读取遇到 `FILE_REFERENCE_EXPIRED` 时按消息号重新取最新引用并重试。

## 限制

- 单文件上限约 2GB（Telegram 文档上限），不做跨消息自动分片，超限直接报错。
- 文件名/目录名最长 255 字节，超长直接拒绝。
- 索引是缓存不是副本：频道消息若被 Telegram 删除，对应文件不可恢复；重要数据请另做备份。
- v0.2.0 起元数据格式改为「节点自述父消息号」，**不兼容 v0.1.x 的旧 caption 与旧索引库**
  （旧频道需一次性迁移脚本，旧索引库删掉后全量重建）。
- 多端同时写同一路径按最后写入生效，不做冲突检测；自动同步窗口约 15 秒。
- Windows 资源管理器路径受 MAX_PATH 260 限制，深层目录建议用面板操作。
- 使用 Telegram 作存储存在账号/服务条款风险，建议专用小号与专用 bot，勿存唯一副本。

## 托盘

`mytgfs-tray.exe` 常驻任务栏：右键菜单可打开面板、按卷快速打开（每卷一项，显示卷名与已用容量）、退出。
开机自启与自动同步开关在面板「设置」页，改完立即生效。
悬停图标显示连接状态、每个卷的文件数与占用、正在下载/排队的任务数。

## 测试

```sh
go build ./...
go vet ./...
go test ./... -cover
```

前端类型检查与推送约束：

```sh
cd web && npm ci && npm run lint
```

桌面端端到端（真实 Wails 窗口 + WebView2 远程调试，需可用账号与卷）：

```sh
go build -tags e2e -o build/mytgfs-tray-e2e.exe ./cmd/mytray
cd web && npm run e2e:desktop
```

`e2e` 构建标签只用于打开浏览器调试端口，正式二进制没有这个开关。
端到端测试是**只读**的：只发 GET 请求核对面板状态，不向 Telegram 账号或频道写入任何数据。

## 元数据格式（贡献者向）

<details>
<summary>点开查看频道里的消息正文格式</summary>

每个节点（文件/目录）是一条消息，正文自述名字与父节点消息号：

- 文件是文档消息，caption 是 `{"v":2,"t":"f","n":"a.txt","p":0,"s":"<sha256>"}`；
- 目录是纯文本消息，正文是 `{"v":2,"t":"d","n":"docs","p":0}`；
- `p` 是父节点消息号，`0` 表示挂在卷根 `/`。卷根是虚拟的，没有根消息、不用置顶；
- 删除标记是 `{"v":2,"t":"x"}`，被标记节点的整棵子树视为已删除；
- 名字限长 255 字节，正文恒小于 Telegram caption/文本上限，不需要拆到别的消息里。

父消息不存在或成环的节点在重建时会挂到卷根。
索引重建从 user 会话翻全量频道历史开始（`mytgfs sync --rebuild`）；
日常增量走频道 pts 差异（`internal/sync`）。
编解码实现在 `internal/dirent`，节点消息读写与对账在 `internal/telegramstore`。

</details>

## 许可证

MIT，见 [LICENSE](LICENSE)。
