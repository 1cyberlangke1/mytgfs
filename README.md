# mytgfs

[![CI](https://github.com/1cyberlangke1/mytgfs/actions/workflows/ci.yml/badge.svg)](https://github.com/1cyberlangke1/mytgfs/actions/workflows/ci.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/1cyberlangke1/mytgfs)](https://github.com/1cyberlangke1/mytgfs/blob/master/go.mod)
[![License](https://img.shields.io/github/license/1cyberlangke1/mytgfs)](https://github.com/1cyberlangke1/mytgfs/blob/master/LICENSE)
[![Release](https://img.shields.io/github/v/release/1cyberlangke1/mytgfs)](https://github.com/1cyberlangke1/mytgfs/releases)

把 Telegram 私有频道当文件存储用的工具。用 bot 通过 MTProto 直连 Telegram，
文件内容存频道，每个节点（文件/目录）是一条消息，消息里自述名字与父节点消息号，
本地 SQLite 只是可重建的索引缓存。改名/移动只编辑节点自己那一条消息，与子树大小无关。

提供三种使用方式：

- **Web 面板**（`mytgfs web`）：浏览器里增删改查、拖拽/目录上传、搜索排序、在线播放、下载到本地目录。
- **系统托盘**（`mytgfs tray`）：常驻后台、自动增量同步，按需打开面板。

> 频道不是专属文件仓库，里面可能有聊天消息。程序**只处理自己格式的消息**（正文含 `v`/`t`/`n`/`p` 字段），
> 不会扫描、删除频道里任何其它消息。删文件只删自己记下的那条消息。

## 环境要求

- Go 1.26.1（从源码构建时）
- Telegram `api_id` / `api_hash`（在 my.telegram.org/apps 申请）
- 一个 bot token（在 @BotFather 申请）

## 构建

```powershell
# Windows：产出 build\mytgfs.exe 与 build\mytgfs-tray.exe
.\build.ps1

# 附带分发 zip（含配置模板、安装脚本）
.\build.ps1 -Package
```

其它平台：

```sh
go build -o mytgfs ./cmd/mytgfs
```

## 安装

1. 解压发行包，在 PowerShell 里执行一次 `.\install.ps1`
   （默认装到 `%LOCALAPPDATA%\mytgfs`，并创建开始菜单快捷方式）。
2. 把 `config.toml`、`secrets/` 配好；登录 bot / user 需要各跑一次 `mytgfs auth`，这两步要用命令行。
3. 之后从开始菜单点 **mytgfs**：启动的是 `mytgfs-tray.exe`（GUI 子系统），
   **不会出现命令行窗口**。右键托盘图标可以打开面板、勾选开机自启。

`mytgfs.exe` 是命令行版（控制台子系统），适合脚本和首次登录；双击它只会停在控制台里等命令。
日常使用请走 `mytgfs-tray.exe` 或开始菜单快捷方式，托盘与面板全程无控制台窗口。

## 快速开始

1. 复制配置模板并填写：

   ```powershell
   Copy-Item config.example.toml config.toml
   ```

   把 `api_id` 填进 `config.toml`；`api_hash` 写进 `secrets/api_hash`、bot token 写进
   `secrets/bot_token`（各单行纯文本）。`config.toml`、`secrets/`、`data/` 都已被 `.gitignore` 忽略，不会提交。

2. 登录 bot（token 从 `secrets/bot_token` 读）：

   ```sh
   mytgfs auth bot
   ```

3. 登录 user 会话（用手机号，建频道/回填历史需要）：

   ```sh
   mytgfs auth user
   ```

4. 建卷（建私有频道、把 bot 拉进频道并提权、关通知）：

   ```sh
   mytgfs volume create --name demo --point Y:
   ```

5. 打开面板：

   ```sh
   mytgfs web
   ```

   浏览器打开提示的本地地址即可。面板里可上传、下载、搜索、播放，下载的文件落在配置的下载目录。

## 命令一览

| 命令 | 作用 |
|---|---|
| `mytgfs version` | 版本与构建信息（支持 `--json`） |
| `mytgfs auth bot` / `auth user` | bot token / 手机号登录 |
| `mytgfs channel init --bot <username>` | 建私有频道并给 bot 提权 |
| `mytgfs volume create --name <n> --point Y:` | 建卷 |
| `mytgfs volume delete --name <n>` | 删卷：删频道、移除配置、删本地索引 |
| `mytgfs volume check` | 检查每个卷的频道是否仍可读写 |
| `mytgfs web [--port N]` | 启动本地 Web 面板（端口默认取 `[web].port`，传 0 用随机端口） |
| `mytgfs tray` | 系统托盘常驻（自动同步、按需面板） |
| `mytgfs ls [--name 卷] [目录]` | 列出目录 |
| `mytgfs mkdir [--name 卷] <目录>` | 创建目录 |
| `mytgfs rm [--name 卷] <路径...>` | 删除文件或目录（目录递归） |
| `mytgfs mv [--name 卷] <源> <目标>` | 移动或改名 |
| `mytgfs get [--name 卷] <远端> [本地\|-]` | 下载文件（`-` 输出到 stdout） |
| `mytgfs search [--name 卷] [--dir 目录] [--limit N] <关键词>` | 按文件名递归搜索 |
| `mytgfs config show\|get\|set\|keys` | 读写配置（与面板配置卡片等价） |
| `mytgfs sync [--name 卷] [--limit N] [--rebuild]` | 增量同步（`--name` 省略时用第一个卷）；`--rebuild` 用 user 会话全量重建 |
| `mytgfs check [--name 卷] [--fix]` | 以频道全量节点为准核对索引；`--fix` 按远端整棵树重建 |
| `mytgfs put [--name 卷] [--remote /目录] <路径...>` | 批量上传文件/目录 |
| `mytgfs cp [--name 卷] [--to 目标卷] [--force] <src> <dst>` | 卷内/跨卷复制（同内容复用引用，零重传） |
| `mytgfs startup enable\|disable\|status` | 开机自启（默认关） |

大部分命令支持 `--json` 输出结构化结果，便于脚本调用；未知子命令以退出码 2 返回用法。
`MYTGFS_LANG` 可强制界面语言（`zh-CN` / `en`）。

## 配置说明

见 `config.example.toml`。要点：

- `[telegram]`：`api_id` 填这里；`api_hash_file` / `bot_token_file` 指向 `secrets/` 下的文件。
- `[mount]`：`read_chunk_mb`（读分块，默认 8）、`download_parallel`（下载连接数，默认 4）、
  `upload_parallel`（上传并发分片，默认 4）、`sync_seconds`（自动同步间隔，默认 15 秒，负数关闭）。
- `[web]`：`port`（面板端口，默认 8720，被占用时自动退回随机端口）。`mytgfs web` 只打印地址，不改浏览器；桌面托盘由 Wails 直接显示面板窗口。
- `[downloads]`：`dir` 是面板「下载」页的落盘目录，留空用 `data/downloads`；可用 `mytgfs config set downloads.dir <路径>` 修改。
- `[log]`：slog + 轮转日志，默认写到 `data/mytgfs.log`。

## 数据都放在哪

- **文件正文 + 元数据**：Telegram 私有频道。每个节点一条消息：
  - 文件是文档消息，caption 是 `{"v":2,"t":"f","n":"a.txt","p":0,"s":"<sha256>"}`；
  - 目录是纯文本消息，正文是 `{"v":2,"t":"d","n":"docs","p":0}`；
  - `p` 是父节点消息号，`0` 表示挂在卷根 `/`。卷根是虚拟的，没有根消息、不用置顶。
  - 名字限长 255 字节，正文恒小于 Telegram caption/文本上限，不需要拆到别的消息里。
- **本地索引**：`data/index-<卷名>.db`（SQLite，WAL）。**可丢弃**：删掉后启动 `web` / `tray`
  会用 user 会话翻整个频道历史自动重建（`RebuildIndexIfEmpty`，按父消息号拼树）；也可手动 `mytgfs sync --rebuild`。
  父消息不存在或成环的节点会挂到卷根。注意 `secrets/` 里的会话与密钥不能删，那是连接 Telegram 的身份。
- **上传断点**：`data/uploads/<sha256>.json`，记录已上传的分片，中断后按内容摘要续传。
- **待补传账本**：写进同一个索引库的 `pending` 表，见下节。
- **会话/密钥**：`secrets/`（bot.session、user.session、session.key 等）。
- **下载目录**：面板「下载」页落盘的位置，默认 `data/downloads`，可用 `[downloads].dir` 改到别的磁盘。

## 写入可靠性与待补传记账

写入流程（面板 / put 共用）：

1. 写打开时把整份内容落到 `data/tmp` 下的临时文件。
2. **关闭时先在 `pending` 表记一笔**（路径、临时文件位置、大小、时间），再上传。
3. 上传并登记索引成功后，清掉这笔账、删临时文件。

如果上传期间崩溃、断网、杀进程，那笔账和临时文件都还在。下次启动
（`web` / `tray`）会自动执行 `ResumePending`：按账本把没传完的文件补传并按内容
sha256 登记索引，成功后清账。临时文件已被清扫或大小对不上时，只清账、不上传，避免把损坏内容写进频道。

分片级的断点续传由 `data/uploads/<sha256>.json` 负责：大文件传到一半中断，重启后跳过已完成分片继续发。

上传成功但索引没写成（库锁、进程被杀）时，上传返回的消息号会立刻记进 `pending_done` 表。
下次启动只需要按这条记账补一次索引，不会把整份内容再传一遍，频道里也不会多出同路径的重复消息；
临时文件内容与记账摘要不一致时视为作废，回退成正常补传。

**写时去重**：提交前按「大小 + sha256」在索引里查找同内容文件，命中就发一条引用已有 document 的
新消息，不再重传字节；复用失败会自动回退成正常上传。删除某个路径只删它自己的消息，其它引用不受影响。
面板里整目录复制同样只发引用消息，不把内容下载再上传一遍。

**从频道重建与 file_reference 刷新**：索引从频道历史重建时，拿到的是上传当时的
`file_reference`，Telegram 的引用会过期。读取遇到 `FILE_REFERENCE_EXPIRED` 时，程序按消息 ID
重新调用 `channels.getMessages` 取最新引用、写回索引并重试一次，因此重建后的旧文件照样能读回。

**中途失败**：改名/移动先改远端消息、再改本地索引；本地写入失败时按已改好的远端正文
重新对账（`reconcileNode`），不会出现「远端叫新名、本地还叫旧名」的分叉视图。
删除时若远端已删、本地清库失败，对账会按「读不到」把本地节点清掉。

**边界**：这套机制只依据本程序自己记下的 `pending` 记录和节点正文工作，
不会因为「频道里有不在索引里的消息」就去删任何东西，聊天消息始终不受影响。

## 传输进度与队列

- 面板的「下载中」显示的是实际已从 Telegram 取回的字节数（单文件 ≥1MB 才展示），读完或关闭文件后自动消失。
- 上传由串行队列提交：单频道限速约 1 条消息/秒，失败按 1/2/4 秒退避重试，面板会显示排队与重试状态。

## 限制

- 单文件上限约 2GB（Telegram 文档上限），不做跨消息自动分片，超限直接报错。
- 文件名/目录名最长 255 字节，超长直接拒绝（正文要塞进一条 caption/文本消息）。
- 索引是缓存，不是副本：频道消息若被 Telegram 删除，对应文件不可恢复；重要数据请另做备份。
- v0.2.0 起元数据格式改为「节点自述父消息号」，**不兼容 v0.1.x 的旧 caption 与旧索引库**。
  旧频道需用一次性迁移脚本把文件消息 caption 改写成节点正文、按需补目录消息（零字节重传），
  旧 `data/index-*.db` 删掉后由程序全量重建；库代码里不含任何旧格式兼容分支。
- 多端同时写同一路径时按最后写入生效（与本地网络盘一致，不做冲突检测）；
  自动同步窗口约 15 秒，其它端在这段时间内看到的是上次同步后的内容。
- Windows 资源管理器路径受 MAX_PATH 260 限制，深层目录建议用面板操作。
- 上传同步占用当前 HTTP 请求；超大文件建议用 `mytgfs put`。
- 使用 Telegram 作存储存在账号/服务条款风险，建议用专用小号与专用 bot，勿存唯一副本。

## 目录结构

```
cmd/mytgfs/     CLI 入口与各子命令
cmd/mytray/     GUI 子系统托盘入口（Windows 双击无黑框）
internal/
  vfs/          卷内文件系统（读写/待补传记账）
  telegramstore/ MTProto 上传、下载、断点续传、引用复制
  index/        SQLite 索引（nodes/meta/pending/pending_done）
  dirent/       节点正文编解码（name/parent/type/sha，名字限长）
  sync/         频道 pts 增量同步、全量历史重建（按父消息号拼树）
  web/          内嵌面板、文件管理、下载目录、缩略图、分片上传
  trayapp/      托盘 + 自动同步 + 按需面板
  ...           auth/channel/config/crypto/fm/queue/uploadcache/...
web/            TypeScript 前端源码（tsc 检查，产物内嵌进 internal/web/dist）
packaging/      安装/卸载脚本
```

## 托盘

`mytgfs-tray.exe`（或 `mytgfs tray`）常驻任务栏，菜单里的常用项：

- **打开面板 / 关闭面板**：按需显示/隐藏 Wails 面板窗口（桌面壳不监听端口）。
- **自动同步**：即时开关频道增量同步，状态写回 `config.toml` 的 `[mount].sync_seconds`。
- **开机自启**：写当前用户的系统注册项，默认关闭。

鼠标悬停在图标上会显示连接状态、每个卷的文件数与占用、正在下载/排队的任务数。

## 测试

```sh
go build ./...
go vet ./...
go test ./... -cover
```

前端类型检查：

```sh
cd web && npm ci && npx tsc --noEmit
```

## 许可证

MIT，见 [LICENSE](LICENSE)。
