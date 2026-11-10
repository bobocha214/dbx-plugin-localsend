# 自动发布 Release

推送 `v版本号` tag 后，GitHub Actions 自动测试、构建并发布 Windows、macOS、Linux 各自的 x64 / ARM64 插件，共六个包。推送普通分支不会发布。

每次发布前：

1. 修改 `manifest.json` 的 `version`、`backend/main.go` 的 `pluginVersion` 和 `ui/index.html` 页脚版本，三处保持一致。打包脚本默认读取 manifest，无需再维护脚本内的版本号。
2. 新增 `docs/RELEASE-版本号.md`，填写本次功能、修复及升级提示。文件内容原样作为 Release 正文，不能只写标题。
3. 提交源码、工作流和更新说明，推送分支和对应 tag。tag 必须指向包含这些内容的提交。

当前 0.1.3 示例（先检查并提交待发布文件）：

```powershell
./scripts/validate-release.ps1 -Tag v0.1.3
git add .github scripts docs backend ui manifest.json README.md LICENSE
git commit -m "release: 邻间 0.1.3"
git push origin main
git tag -a v0.1.3 -m "邻间 0.1.3"
git push origin v0.1.3
```

更新说明示例：

```markdown
# 邻间 0.1.3

- 新增：描述新增功能。
- 修复：描述修复的问题。
- 使用提示：如有升级限制，在这里说明。
```

在仓库 Actions 查看运行结果，Releases 查看发布内容。版本或更新说明不匹配、测试失败都会停止发布。Go 版本从 `backend/go.mod` 读取，调用现有打包脚本生成安装包。

如果 tag 已推送但没有触发运行，可在 Actions → Publish DBX plugin → Run workflow 中输入已存在的 tag，或运行 `gh workflow run release.yml --ref main -f tag=v0.1.3`。该入口会检出对应 tag 的源码并执行同样的版本检查，无需删除或移动 tag。六平台工作流要求对应 tag 已包含本次跨平台构建脚本，不能用于补建此前只有 Windows 打包脚本的旧 tag。

工作流分别在 Windows、macOS、Linux runner 上执行本机测试，再交叉编译该系统的两个架构；ARM64 与 x64 包的生成不代表已经在两种架构的真机上运行。所有构建完成并校验成功后才创建公开 Release。

每次 Release 为以下六个 target 分别附带三个文件，共 18 个附件：

| 系统 | Intel / AMD 64 位 | ARM 64 位 |
| --- | --- | --- |
| Windows | windows-x64 | windows-arm64 |
| macOS | macos-x64 | macos-arm64 |
| Linux | linux-x64 | linux-arm64 |

- `io.github.yuzhi.localsend-版本号-target.dbxp`：DBX 插件安装包。
- 同名 `.dbxp.sha256`：安装包 SHA-256 校验值。
- 同名 `.artifact.json`：文件名、平台、大小和校验值元数据。

使用仓库自带的 `GITHUB_TOKEN`，无需个人访问令牌。仓库需启用 GitHub Actions，并允许工作流使用 `contents: write`；组织策略可能限制该权限。

本地打包需 Go、PowerShell 7 和 Python 3.8+。例如 `./scripts/package-im.ps1 -Target macos-arm64`；省略 Target 时保留 windows-x64 默认值。输出文件在 dist 内，运行 `python scripts/verify-package.py dist/具体文件.dbxp` 验证。脚本通过 GOOS / GOARCH 交叉编译，无需在目标系统上打包。

Release 先创建为草稿，附件上传后才公开。已有同名 Release 时重新运行会更新说明并覆盖同名附件，便于失败后重试。不要移动已经公开发布的 tag。

预发布 tag 如 `v0.2.0-beta.1` 会标记为 Pre-release，三处版本和说明文件名也需包含 `-beta.1`。仅在本地创建 tag 不会触发发布。
