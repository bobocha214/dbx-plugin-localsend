# 邻间 0.2.0

免登录局域网通讯：插件自动进入局域网大厅，网页游客只需昵称即可聊天、传图片和文件；支持插件联系人、私聊与可选中心服务。

当前版本为 **0.2.0**。使用、数据与验证说明见 [docs/IM.md](docs/IM.md)，发布说明见 [docs/RELEASE-0.2.0.md](docs/RELEASE-0.2.0.md)。

支持 Windows / macOS / Linux 的 x64（AMD64）和 ARM64。构建安装包需 Go、PowerShell 7 和 Python 3.8+；运行 `./scripts/package-im.ps1 -Target darwin-arm64` 可指定目标，省略时生成 Windows x64 包，输出位于 dist。

自动发布：推送 `v版本号` tag 后，GitHub Actions 自动测试、打包并创建 Release，更新内容读取 `docs/RELEASE-版本号.md`。操作步骤见 [发布指南](docs/RELEASING.md)。

## 📝 License

[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

Apache License 2.0，见 [LICENSE](LICENSE)。
