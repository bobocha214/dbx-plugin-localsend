# 邻间 0.1.4

- 更名：macOS 安装包由 `macos-x64` / `macos-arm64` 更名为 `darwin-x64` / `darwin-arm64`，与 Go 的 GOOS（darwin）命名一致；打包、校验脚本与发布工作流同步更新。
- 安装包内容不变，仅文件名变更；Windows 与 Linux 包名不受影响。

选择包：Intel / AMD 64 位电脑使用 x64；Apple Silicon、Windows ARM 和 Linux ARM 64 位设备使用 arm64。macOS 安装包使用 `darwin-x64` / `darwin-arm64` 命名，darwin 即 macOS，x64 对应 amd64。

升级提示：从 0.1.3 升级无需迁移数据，直接安装覆盖即可。0.1.3 及更早版本的 macOS 包仍以 macos-* 命名发布，历史下载链接不变。

验证范围：在 Windows 本机执行后端与前端回归测试，并交叉构建和静态校验六个平台包（含更名后的 darwin 包）。
