# 邻间 0.1.3

- 新增 Apache License 2.0 授权：仓库根目录附带 `LICENSE`，安装包内也包含该文件，许可证为 Apache-2.0。
- 安装包新增 LICENSE 后，包内校验清单（checksums.json）同步覆盖，`verify-package.py` 校验逻辑不变。
- 文档版本号修正：`docs/IM.md` 标题与 README 示例统一对齐到 0.1.3。

选择包：Intel / AMD 64 位电脑使用 x64；Apple Silicon、Windows ARM 和 Linux ARM 64 位设备使用 arm64。包名中 macos 对应 Go 的 darwin，x64 对应 amd64，即 macOS 安装包使用 `macos-x64` / `macos-arm64` 命名。

升级提示：从 0.1.2 升级无需迁移数据，直接安装覆盖即可。

验证范围：在 Windows 本机执行后端与前端回归测试，并交叉构建和静态校验六个平台包；LICENSE 文件包含在各平台包的校验中。
