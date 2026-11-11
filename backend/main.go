// DBX sidecar for 邻间（局域网 IM）。
//
// Methods:
//   im/status | start | stop | connect | request | storage | pick-directory | archive-*
//   connection/test | connect | disconnect
//   industrial/ping

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"
)

const (
	pluginID      = "io.github.yuzhi.localsend"
	pluginVersion = "0.2.1"
	defaultPort   = localsend.DefaultPort
	defaultScheme = "http"
)

// Plugin 共享状态
type Plugin struct {
	imState imRuntime
	mu      sync.RWMutex
	dataDir string // DBX 提供的插件数据目录，IM 数据存放在其 im/ 子目录
}

func NewPlugin() *Plugin { return &Plugin{} }

func main() {
	plugin := NewPlugin()

	// 插件数据目录由 DBX 提供（环境变量 DBX_PLUGIN_DATA_DIR），
	// 插件从临时解包目录启动时同级目录里的数据会丢失，必须写在这里。
	dataDir, err := dbxpluginsdk.EnsureDataDir()
	if err != nil || dataDir == "" {
		if exe, exeErr := os.Executable(); exeErr == nil {
			dataDir = filepath.Dir(exe)
		} else {
			dataDir = os.TempDir()
		}
	}
	plugin.dataDir = dataDir

	metadata := dbxpluginsdk.Metadata{ID: pluginID, Version: pluginVersion, Capabilities: []string{"connections", "localsend"}}
	server := dbxpluginsdk.NewServer(metadata, plugin)
	if err := server.Serve(); err != nil {
		fmt.Fprintf(os.Stderr, "[%s] %v\n", pluginID, err)
		os.Exit(1)
	}
}

// --- Handler ---

func (p *Plugin) Handle(
	_ dbxpluginsdk.RequestContext, method string, params json.RawMessage, _ *dbxpluginsdk.Emitter,
) (any, *dbxpluginsdk.PluginError) {
	if strings.HasPrefix(method, "im/") {
		return p.handleIM(method, params)
	}
	switch method {
	case "connection/test":
		return map[string]any{"success": true, "message": "局域网 IM 本地节点可用"}, nil
	case "connection/connect":
		if _, err := p.handleIM("im/status", json.RawMessage(`{}`)); err != nil {
			return nil, err
		}
		return map[string]any{"success": true}, nil
	case "connection/disconnect":
		// Workbench sessions share the node; disconnecting one must not stop other users.
		return map[string]any{"success": true}, nil
	case "industrial/ping":
		return map[string]any{"ok": true, "plugin": pluginID, "version": pluginVersion}, nil
	default:
		return nil, dbxpluginsdk.MethodNotFound(method)
	}
}

var _ dbxpluginsdk.Handler = (*Plugin)(nil)
var _ io.Reader = nil
