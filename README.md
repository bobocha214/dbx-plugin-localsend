# 邻间 0.2.1

DBX 局域网通讯插件。免登录收发消息、图片、文件和文件夹，同时提供完整网页客户端；支持 Windows、macOS、Linux 的 x64（AMD64）和 ARM64。

## 使用

1. 在 DBX 安装对应平台的 `.dbxp` 包，打开“邻间”工作台，本地服务自动启动。
2. 同事在局域网内通过分享地址或二维码打开网页，填写昵称即可加入大厅。
3. 从通讯录开始私聊、群聊，从会话列表右上角“＋”创建群聊。
4. 在设置中心调整主题、字体、紧凑布局、存储位置和连接服务。

大厅对同一服务全部访客开放。群聊仅指定成员可访问，默认全员可发言上传；可切换为仅群主与指定成员发布，并按需生成限时、限人数邀请。新成员可读取全部群聊历史。昵称是显示名称，不代表真实身份认证。

## 群聊与文件管理

群聊默认进入聊天，提供“聊天 / 文件 / 成员”视图，可修改名称、简介、成员和权限，也可归档或解散。文件页支持按名称搜索、文档/图片分类、排序、分页、选中下载和真实下载记录。每个邀请地址独立提供复制和二维码。

成员移除后服务端立即拒绝新的访问请求，网页在轮询后清理已打开内容；已保存到设备的文件无法远程收回。解散群聊撤销访问，保留服务器文件供管理员备份处理。

## 开发与打包

需要 Go（版本见 `backend/go.mod`）、Node.js、PowerShell 7 和 Python 3.8+。

```powershell
cd backend
go test ./... -count=1
go vet ./...
cd ..
node ui/im.test.js
node ui/im-settings.test.js
node ui/im-theme.test.js
node ui/im-transfer.test.js
node ui/im-channels.test.js
./scripts/package-im.ps1 -Target windows-x64
python scripts/verify-package.py dist/io.github.yuzhi.localsend-0.2.1-windows-x64.dbxp
```

可选 target：`windows-x64`、`windows-arm64`、`macos-x64`、`macos-arm64`、`linux-x64`、`linux-arm64`。安装包输出在 `dist/`，附 SHA-256 和 artifact 元数据。

推送版本 tag 后，GitHub Actions 测试并构建六个平台，全部通过后创建 Release，更新内容读取对应版本文档。

- [使用与数据说明](docs/IM.md)
- [0.2.1 更新内容](docs/RELEASE-0.2.1.md)
- [发布指南](docs/RELEASING.md)

当前后端位于 `backend/im/`，前端使用 `ui/index.html`、`ui/im.js`、`ui/im.css`。`backend/localsend/` 与 `ui/legacy.html` 为旧版参考，新工作台不自动启动旧频道服务。
