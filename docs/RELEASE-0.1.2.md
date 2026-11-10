# 邻间 0.1.2

- 新增 Windows、macOS 和 Linux 的 x64（AMD64）及 ARM64 安装包，共六个平台。
- 自动发布工作流在三个操作系统上运行原生测试，并交叉编译对应的 x64 / ARM64 包；六个平台全部构建并校验通过后统一发布。
- macOS 使用原生目录选择框；Linux 支持 zenity 或 kdialog 目录选择，未安装时可手动填写存储路径。
- 打包脚本支持 `-Target` 指定平台，macOS / Linux 包携带 Unix 可执行权限元数据。
- 每个平台独立附带 SHA-256 校验文件和 artifact 元数据；验证脚本检查二进制架构、包内校验、路径和执行权限。

选择包：Intel / AMD 64 位电脑使用 x64；Apple Silicon、Windows ARM 和 Linux ARM 64 位设备使用 arm64。包名中 macos 对应 Go 的 darwin，x64 对应 amd64。

Linux 图形目录选择需桌面会话及 zenity 或 kdialog；没有图形环境时仍可手动设置存储目录。macOS / Linux 运行需要宿主 DBX 支持对应系统及架构。本次不包含 Apple Developer ID 签名或 notarization。

验证范围：在 Windows 本机执行后端与前端回归测试，并交叉构建和静态校验六个平台包。macOS / Linux GUI 目录选择及 ARM64 真机安装仍需对应设备验证，不能以交叉编译替代。
