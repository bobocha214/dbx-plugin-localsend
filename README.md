# 邻间 0.1.0

当前版本默认免登录：插件自动进入局域网大厅，网页游客只需昵称即可聊天、传图片和文件；保留插件联系人、私聊与可选中心服务。

当前版本重新定为 **0.1.0**。使用、数据与验证说明见 [docs/IM.md](docs/IM.md)，发布说明见 [docs/RELEASE-0.1.0.md](docs/RELEASE-0.1.0.md)。

构建安装包：在项目根目录运行 `./scripts/package-im.ps1`，输出 `dist/io.github.yuzhi.localsend-0.1.0-windows-x64.dbxp`。

下面内容为旧版频道实现参考，新工作台不再自动启动旧频道 HTTP 服务。

---

# 局域网快传 (LocalSend for DBX)

> 基于 DBX 插件协议的局域网点对点文件传输与频道聊天工具，零中间服务器。

[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.27%2B-00ADD8.svg)](https://go.dev)
[![Platform](https://img.shields.io/badge/platform-windows_x64-lightgrey.svg)](#构建与打包)

---

## ✨ 功能特性

- **🔒 频道模型**：公有频道（无 PIN）直接加入、私有频道（6 位 PIN）进入；所有频道共享一个 HTTP 端口
- **📡 局域网发现**：基于 LocalSend v2 UDP 多播协议（`224.0.0.167:53317`）自动发现同网段在线设备
- **💬 聊天式交互**：频道内文字消息 + 文件卡片；自己的消息右对齐、对方消息左对齐；消息自带稳定设备 ID，刷新后仍能正确识别
- **📦 文件收发**：主界面通过插件桥接 RPC 上传/下载（避开 WebView CORS 拦截），网页端走同源 HTTP
- **🖼 分享方式**：访问根路径进入频道选择页（浏览器访问 `http://<IP>:<端口>`），复制链接或扫码直接进入
- **📁 文件操作**：下载、用系统默认程序直接打开、在资源管理器定位并选中（网页端也能触发宿主操作）
- **💾 持久化**：频道元数据、消息历史、传输记录、文件本体全部落盘到用户选定的保存目录；插件配置写到 DBX 提供的 `DBX_PLUGIN_DATA_DIR`（跨重启不丢）
- **🎨 双端 UI**：DBX 插件内嵌 WebView 主界面 + 独立浏览器端 SPA，风格统一、响应式布局

---

## 🚀 快速上手

### 从 `.dbxp` 安装（推荐）

1. 打开 DBX → 设置 → 插件 → 安装本地插件
2. 选择 [最新发布包](#构建与打包) 中的 `io.github.yuzhi.localsend-0.1.0-windows-x64.dbxp`
3. 打开「局域网快传」工作台 → 右上角「启动」
4. 创建/加入频道 → 开始聊天或发送文件

### 从源码运行

```bash
# 后端
cd backend
go build -ldflags="-s -w" -o dbx-plugin-localsend.exe .

# 打包成 .dbxp（Windows PowerShell，见下方完整流程）
```

---

## 🏗 项目结构

```
dbx-plugin-localsend/
├── manifest.json              # DBX 插件清单（schema v1）
├── dbx-plugin.toml            # DBX 构建配置（language=go）
├── assets/
│   └── plugin.svg              # 插件图标（内联到所有前端页面）
├── ui/
│   ├── index.html              # DBX 内嵌 WebView 主界面（Vue-less 原生 JS）
│   └── vendor/
│       └── qrcode.min.js       # qrcodejs（构造函数式，非 QRCode.toCanvas）
├── backend/
│   ├── main.go                 # RPC 分发入口 + 启动流程（RestoreFromDisk → CreateDefaultLobby → AutoStart）
│   ├── localsend/
│   │   ├── server.go           # HTTP 服务：频道选择页、visitor SPA、/api/channels/*、LocalSend v2 路由、打开宿主文件
│   │   ├── channel.go          # Channel 结构体 + AppendMessageFrom + SaveMeta + FileByID + RestoreFromDisk
│   │   ├── discovery.go        # UDP 多播 announce（224.0.0.167:53317）+ 扫描
│   │   ├── client.go           # LocalSend 协议发送端（send-request → upload）
│   │   ├── transfers.go        # 传输记录持久化（sent-*.json / received-*.json）
│   │   ├── interfaces.go       # 网卡枚举 + 自动选最优网卡
│   │   └── types.go            # RegisterRequest / SendRequest / FileMetadata / ChannelMeta 等
└── dist/                       # .dbxp 打包产物（已加入 .gitignore）
```

### 架构一览

```
DBX 内嵌 WebView ──stdin/stdout JSON-RPC──▶ backend/main.go
   │                                            │
   │  ui/index.html                             ├── localsend/server.go ──▶ 0.0.0.0:<port>
   │  (getSelfId() → lc_self_id)               │                          │
   │                                            │                          ├── GET  /               → 频道选择页 (handleRoot)
   │                                            │                          ├── GET  /c/<id>        → 频道 visitor SPA
   │                                            │                          ├── GET  /api/channels      → 列表（private 擦除 PIN）
   │                                            │                          ├── GET  /api/channels/<id>/messages
   │                                            │                          ├── POST /api/channels/<id>/messages
   │                                            │                          ├── POST /api/channels/<id>/files        → 上传
   │                                            │                          ├── GET  /api/channels/<id>/files[/<fileID>] → 下载
   │                                            │                          ├── POST /api/open-file            → 宿主打开文件
   │                                            │                          ├── POST /api/open-folder          → 宿主打开资源管理器
   │                                            │                          └── LocalSend v2 路由（/register /send-request /upload /info）
   │                                            │
   │                                            ├── localsend/discovery.go ──▶ UDP 224.0.0.167:53317 多播
   │                                            ├── localsend/channel.go   ──▶ saveDir/chat-records/<id>.json
   │                                            └── localsend/transfers.go  ──▶ saveDir/transfers/*.json
```

### 单端口架构

所有频道共享 `cfg.Port`（默认 53317），主 Server 监听 `0.0.0.0`，通过 URL 路径路由：

| 路径 | 响应 |
|---|---|
| `/` | 频道选择页（无频道时显示空状态） |
| `/c/<channelID>` | 该频道的聊天 SPA（含六格 PIN 输入） |
| `/api/channels` | 频道列表（private 频道只返回元数据） |
| `/api/channels/<id>/messages` | GET 拉历史 / POST 发消息 |
| `/api/channels/<id>/files` | POST 上传 / GET 列出 |
| `/api/channels/<id>/files/<fileID>` | GET 下载 |
| `/api/open-folder` | 网页端触发宿主资源管理器 |
| `/api/open-file` | 网页端触发宿主默认程序 |
| `/api/localsend/v2/*` | LocalSend v2 协议 |

### 消息设备 ID 约定

为了刷新后仍能识别「自己发的消息」，每条消息带 `deviceId`：

- **DBX 主界面**：后端注入本机 `cfg.UniqueID`（MAC@IP，稳定）
- **网页端**：localStorage 生成 `lc_cid`，发消息时带上
- **消息匹配**：`m.deviceId === state.config.uniqueId`（主界面）或 `m.deviceId === myCid`（网页端）

旧消息（无 `deviceId`）降级按昵称 `sender` 匹配。

---

## 📡 RPC 接口（DBX → 插件）

所有调用走 `{"jsonrpc":"2.0","method":"...","params":{...},"id":n}`。

### 配置

| 方法 | 说明 |
|---|---|
| `config/get` | 返回当前配置 + 本机 IP |
| `config/set` | 部分字段 patch（nickname/port/protocol/saveDir/networkInterface/autoStart）；自动刷新 UniqueID 并持久化 |
| `config/start` | 启动服务（停旧 → 启新） |
| `config/stop` | 停服务 |
| `config/restart` | 重启 |
| `config/status` | 运行状态快照 |

### 频道

| 方法 | 说明 |
|---|---|
| `channel/list` | 列表 |
| `channel/create` | 创建（private 空 PIN 时自动生成 6 位）；立即落盘元数据 |
| `channel/update` | 更新名称/PIN/可见性；立即落盘 |
| `channel/delete` | 删除（内存 + 磁盘） |
| `channel/join` | 加入（带 PIN 校验） |
| `channel/leave` | 离开 |
| `channel/message` | 发消息（后端自动注入 `deviceId = cfg.UniqueID`） |
| `channel/messages` | 拉历史 |
| `channel/add-file` | 通过 base64 上传文件到 `saveDir/channel-files/<id>/` |
| `channel/remove-file` | 移除文件元数据 |
| `channel/upload-file` | 主界面专用：base64 上传（带超时放宽到 120s） |
| `channel/read-file` | 主界面专用：base64 下载（WebView 无法直接 fetch 本地接口时使用） |
| `channel/share-url` | 返回该频道的访问地址 |

### 系统

| 方法 | 说明 |
|---|---|
| `system/open-url` | 用系统默认浏览器打开 URL（Windows 用 `rundll32 url.dll,FileProtocolHandler`） |
| `system/open-file` | 用系统默认程序打开频道内文件（Windows 用 `cmd /c start "" <path>`） |
| `system/reveal-file` | 资源管理器定位并选中（`explorer.exe /select,<path>`） |

### LocalSend / 局域网

| 方法 | 说明 |
|---|---|
| `localsend/interfaces` | 网卡列表 |
| `localsend/scan` | 主动扫描在线设备 |
| `localsend/devices` | 当前发现的设备列表 |
| `localsend/send` | 向指定设备发送文件（send-request → upload） |
| `localsend/transfers` | 传输记录列表（sent-*.json / received-*.json） |

---

## 🛠 构建与打包

### 本地构建

```bash
cd backend
go build -ldflags="-s -w" -o dbx-plugin-localsend.exe .
```

### 打包成 `.dbxp`

```powershell
$stage = 'dist\.stage-io.github.yuzhi.localsend-windows-x64'

# 1. 构建
cd backend
go build -ldflags="-s -w" -o dbx-plugin-localsend.exe .
cd ..

# 2. 拷贝产物到 stage
Copy-Item backend\dbx-plugin-localsend.exe  "$stage\bin\windows-x64\dbx-plugin-localsend.exe" -Force
Copy-Item ui\index.html                    "$stage\ui\index.html" -Force

# 3. 生成 checksums.json
$files = @(
    'assets/plugin.svg',
    'bin/windows-x64/dbx-plugin-localsend.exe',
    'manifest.json',
    'ui/index.html',
    'ui/vendor/qrcode.min.js'
)
$map = [ordered]@{}
foreach ($f in $files) {
    $p = Join-Path $stage ($f -replace '/', '\')
    $map[$f] = (Get-FileHash $p -Algorithm SHA256).Hash.ToLower()
}
@{ algorithm = 'sha256'; files = $map } | ConvertTo-Json -Depth 5 |
    Set-Content -Path "$stage\checksums.json" -Encoding UTF8

# 4. 打 zip（DBX 要求的 .dbxp 格式其实就是 zip）
Remove-Item 'dist\io.github.yuzhi.localsend-0.1.0-windows-x64.dbxp' -Force -ErrorAction SilentlyContinue
Compress-Archive -Path "$stage\*" -DestinationPath 'dist\io.github.yuzhi.localsend-0.1.0-windows-x64.dbxp' -Force

# 5. 算 SHA256（发布时带上）
Get-FileHash 'dist\io.github.yuzhi.localsend-0.1.0-windows-x64.dbxp' -Algorithm SHA256
```

### `.dbxp` 内部结构

```
io.github.yuzhi.localsend-0.1.0-windows-x64.dbxp (zip)
├── manifest.json
├── checksums.json        # {"algorithm":"sha256","files":{"<path>":"<hash>"}}
├── assets/
│   └── plugin.svg
├── bin/
│   └── windows-x64/
│       └── dbx-plugin-localsend.exe
└── ui/
    ├── index.html
    └── vendor/
        └── qrcode.min.js
```

---

## 📁 数据落盘

| 内容 | 路径 |
|---|---|
| 插件配置 | `$DBX_PLUGIN_DATA_DIR/config.json`（环境变量由 DBX 注入，取不到时回退 exe 同级目录 / 系统临时目录；首次启动自动迁移旧位置） |
| 频道元数据 + 消息历史 | `<saveDir>/chat-records/<channelID>.json` |
| 传输记录 | `<saveDir>/transfers/sent-<sessionID>.json` / `received-<sessionID>.json` |
| 频道内文件 | `<saveDir>/channel-files/<channelID>/...` |

`saveDir` 默认是 `~/Documents/LocalSend`，可在工作台设置里改。

---

## 🔧 开发说明

### 关键设计决策

1. **配置路径必须走 DBX 提供的环境变量**：`filepath.Dir(os.Executable())` 每次启动都是新的临时解包目录，配置放那儿会丢。
2. **前端身份必须有稳定 ID**：LocalStorage 能拿到就持久化，拿不到就生成一个内存 ID，但消息体上带后端注入的 `deviceId` 才能跨刷新识别。
3. **主界面文件收发走 RPC 不走 fetch**：DBX WebView 对 `http://127.0.0.1:<port>/api/...` 这类跨 origin 请求可能整体拦截；base64 经桥接绕开此限制。
4. **qrcodejs 不是 `QRCode.toCanvas`**：只暴露 `new QRCode(element, { text, width, height })` 构造函数，别用 `QRCode.toDataURL`。
5. **LocalSend v2 的 `send-request` 返回 `status: "accept"`**，不是 `"ok"`，前端/后端判定必须同时兼容两者。

### 常见问题

| 现象 | 排查方向 |
|---|---|
| 启动后关闭页面再进点启动无效 | 上次监听 TIME_WAIT；server 里 Listen 有 10×200ms 重试。确认端口未被占用 |
| 私有频道重启后消失 | 旧代码没调 `SaveMeta()`，只 `AppendMessage` 才落盘。已修复为创建/更新都调 |
| WebView 点「浏览器打开」无反应 | 拦截 `window.open`；走 `system/open-url` RPC（宿主用 `rundll32` 打开） |
| 文件发出去但对方收不到 | 检查 `saveDir/channel-files/<id>/` 是否真有文件；网页端 fetch 同源、主界面走 RPC |
| 刷新后自己发的消息跑到左边 | 消息体缺 `deviceId`；主界面必须注入 `cfg.UniqueID`，网页端用 localStorage 的 `lc_cid` |

---

## 📦 发布记录

### v0.1.0

- 频道模型（公有/私有 + 6 位 PIN）
- LocalSend v2 局域网发现与点对点发送
- 单端口多频道
- visitor SPA（网页端访问 `http://<IP>:<port>`）
- 消息/传输/文件持久化
- 分享面板：链接、复制、二维码、浏览器打开
- 右侧频道文件：打开 / 打开文件夹 / 移除
- DBX 主界面与网页端都支持通过宿主程序打开文件
- 配置写入 DBX 数据目录（重启不丢）
- 内联 plugin.svg 作为所有前端页面的品牌图标

---

## 📝 License

MIT
