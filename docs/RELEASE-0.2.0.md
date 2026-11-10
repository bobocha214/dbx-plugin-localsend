# 邻间 0.2.0

- 移除旧版频道服务：删除后端 `localsend` 包（频道模型、LocalSend v2 局域网快传与设备发现）和旧版网页界面 `ui/legacy.html`，以及旧版 RPC 入口 `config/*`、`channel/*`、`localsend/*`、`system/*`、`folder/pick`、`share/all-addresses`。
- 插件聚焦新的局域网 IM 工作台：后端仅保留 `im/*`、`connection/*` 与 `industrial/ping`，网页入口、游客大厅、单聊群聊、图片文件与可信节点直连不受影响。
- 旧版频道数据（聊天记录、频道文件）不会被迁移或删除，仍保留在原保存目录中；新 IM 数据位于 `$DBX_PLUGIN_DATA_DIR/im/`。

选择包：Intel / AMD 64 位电脑使用 x64；Apple Silicon、Windows ARM 和 Linux ARM 64 位设备使用 arm64。macOS 安装包使用 `darwin-x64` / `darwin-arm64` 命名，darwin 即 macOS，x64 对应 amd64。

升级提示：从 0.1.x 升级无需迁移数据，直接安装覆盖即可；如需查看旧频道数据，请改用 0.1.x 版本安装包，新 IM 不读取旧数据。

验证范围：在 Windows 本机执行后端 `go test ./...`、`go vet ./...` 与前端 Node 回归测试，并交叉构建和静态校验六个平台包。
