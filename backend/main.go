// DBX sidecar for 局域网快传.
//
// Methods:
//   config/get | set | start | stop | restart | status
//   localsend/interfaces | devices | scan | send | ping | info
//   folder/pick — 弹系统目录选择对话框（Windows 走 PowerShell）
//   channel/create | delete | list | join | leave | message | messages | add-file | remove-file | share-url

package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bobocha214/dbx-plugin-localsend/localsend"
	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"
)

const (
	pluginID      = "io.github.yuzhi.localsend"
	pluginVersion = "0.1.3"
	defaultPort   = localsend.DefaultPort
	defaultScheme = "http"
)

// Config 持久化到 config.json
type Config struct {
	Nickname      string `json:"nickname"` // 自定义昵称，给人看的
	UniqueID      string `json:"uniqueId"` // 机器唯一 ID（MAC@IP），自动生成
	MAC           string `json:"mac"`      // MAC 地址本身，调试用
	IP            string `json:"ip"`       // 当前选定网卡的本机 IP
	Port          int    `json:"port"`
	Protocol      string `json:"protocol"`
	SaveDir       string `json:"saveDir"`
	NetworkIfSpec string `json:"networkInterface"`
	AutoStart     bool   `json:"autoStart"`
}

func defaultConfig() Config {
	iface := localsend.SelectBestInterface()
	mac := localsend.MACAddress(iface)
	ip := localsend.FirstIPv4(iface)
	return Config{
		Nickname:      autoNickname(),
		UniqueID:      localsend.DeviceID(mac, ip),
		MAC:           mac,
		IP:            ip,
		Port:          defaultPort,
		Protocol:      defaultScheme,
		SaveDir:       defaultSaveDir(),
		NetworkIfSpec: "auto",
		AutoStart:     false,
	}
}

func defaultSaveDir() string {
	// 用户主目录下 Documents/LocalSend
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, "Documents", "LocalSend")
}

func autoNickname() string {
	h, _ := os.Hostname()
	if h == "" {
		h = "DBX"
	}
	return h
}

// Plugin 共享状态
type Plugin struct {
	imState          imRuntime
	mu               sync.RWMutex
	cfg              Config
	cfgPath          string
	localFingerprint string
	localIP          string
	localMAC         string

	running   bool
	discovery *localsend.Discovery
	server    *localsend.Server

	channels map[string]*localsend.Channel

	// 传输记录存储（每个 server 独立落盘到自己的 saveDir/transfers/）
	transferStore *localsend.TransferStore

	// emitter 保存最新一次 Handle 调用的 Emitter，用于异步推事件给前端
	emitter *dbxpluginsdk.Emitter
}

func NewPlugin() *Plugin {
	return &Plugin{
		cfg:              defaultConfig(),
		localFingerprint: genFingerprint(),
		channels:         make(map[string]*localsend.Channel),
	}
}

func genFingerprint() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func main() {
	plugin := NewPlugin()

	// config.json 必须放在 DBX 提供的插件数据目录（环境变量 DBX_PLUGIN_DATA_DIR），
	// 否则插件每次从临时解包目录启动，同级目录里的配置会丢失。
	cfgDir, err := dbxpluginsdk.EnsureDataDir()
	if err != nil || cfgDir == "" {
		if exe, exeErr := os.Executable(); exeErr == nil {
			cfgDir = filepath.Dir(exe)
		} else {
			cfgDir = os.TempDir()
		}
	} else if exe, exeErr := os.Executable(); exeErr == nil {
		// 迁移旧版本写在 exe 同级目录的配置（只迁移一次）
		oldPath := filepath.Join(filepath.Dir(exe), "config.json")
		newPath := filepath.Join(cfgDir, "config.json")
		if data, readErr := os.ReadFile(oldPath); readErr == nil {
			if _, statErr := os.Stat(newPath); os.IsNotExist(statErr) {
				_ = os.WriteFile(newPath, data, 0o644)
			}
		}
	}
	plugin.cfgPath = filepath.Join(cfgDir, "config.json")

	if err := plugin.loadConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "[%s] load config: %v\n", pluginID, err)
	}

	// 用选定网卡的 MAC/IP 生成唯一 ID（每次启动都刷新）
	plugin.refreshIdentity()

	// 恢复落盘的频道
	os.MkdirAll(plugin.cfg.SaveDir, 0o755)
	restored := localsend.RestoreFromDisk(
		plugin.cfg.SaveDir, plugin.localIP, plugin.cfg.Nickname, plugin.cfg.UniqueID,
	)
	for _, ch := range restored {
		plugin.channels[ch.ID] = ch
	}
	// 默认公有频道（如果恢复里没有就建一个）
	lobbyExists := false
	for _, ch := range plugin.channels {
		if ch.Visibility == localsend.VisibilityPublic && (ch.Name == "公有频道" || ch.Name == "大厅") {
			lobbyExists = true
			break
		}
	}
	if !lobbyExists {
		lobby := localsend.DefaultLobby(plugin.localIP, plugin.cfg.SaveDir, plugin.cfg.Nickname, plugin.cfg.UniqueID)
		plugin.channels[lobby.ID] = lobby
	}

	// Legacy service is opt-in only; saved v0.1 settings must not expose its
	// unauthenticated HTTP endpoints when the new IM workbench opens.

	metadata := dbxpluginsdk.Metadata{ID: pluginID, Version: pluginVersion, Capabilities: []string{"connections", "localsend"}}
	server := dbxpluginsdk.NewServer(metadata, plugin)
	if err := server.Serve(); err != nil {
		fmt.Fprintf(os.Stderr, "[%s] %v\n", pluginID, err)
		os.Exit(1)
	}
}

// refreshIdentity 用选定网卡刷新 MAC/IP/UniqueID
func (p *Plugin) refreshIdentity() {
	iface, _ := localsend.PickInterface(p.cfg.NetworkIfSpec)
	p.localIP = localsend.FirstIPv4(iface)
	p.localMAC = localsend.MACAddress(iface)
	p.cfg.IP = p.localIP
	p.cfg.MAC = p.localMAC
	p.cfg.UniqueID = localsend.DeviceID(p.localMAC, p.localIP)
}

// --- config 持久化 ---

func (p *Plugin) loadConfig() error {
	data, err := os.ReadFile(p.cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var loaded Config
	if err := json.Unmarshal(data, &loaded); err != nil {
		return err
	}
	merged := defaultConfig()
	if loaded.Nickname != "" {
		merged.Nickname = loaded.Nickname
	}
	if loaded.Port != 0 {
		merged.Port = loaded.Port
	}
	if loaded.Protocol != "" {
		merged.Protocol = loaded.Protocol
	}
	if loaded.SaveDir != "" {
		merged.SaveDir = loaded.SaveDir
	}
	if loaded.NetworkIfSpec != "" {
		merged.NetworkIfSpec = loaded.NetworkIfSpec
	}
	merged.AutoStart = loaded.AutoStart
	p.cfg = merged
	return nil
}

func (p *Plugin) saveConfig() error {
	data, err := json.MarshalIndent(p.cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p.cfgPath, data, 0o644)
}

// --- Handler ---

func (p *Plugin) Handle(
	_ dbxpluginsdk.RequestContext, method string, params json.RawMessage, em *dbxpluginsdk.Emitter,
) (any, *dbxpluginsdk.PluginError) {
	if strings.HasPrefix(method, "im/") {
		return p.handleIM(method, params)
	}
	// 保存最新 emitter（后台 goroutine 推事件用）
	if em != nil {
		p.mu.Lock()
		p.emitter = em
		p.mu.Unlock()
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
	case "config/get":
		return p.configGet(), nil
	case "config/set":
		return p.configSet(params)
	case "config/start":
		return p.configStart()
	case "config/stop":
		return p.configStop()
	case "config/restart":
		return p.configRestart()
	case "config/status":
		return p.configStatus(), nil
	case "localsend/interfaces":
		return p.localsendInterfaces(), nil
	case "localsend/devices":
		return p.localsendDevices(), nil
	case "localsend/scan":
		return p.localsendScan(), nil
	case "localsend/send":
		return p.localsendSend(params)
	case "localsend/ping":
		return p.localsendPing(params)
	case "localsend/transfers":
		return p.localsendTransfers(params)
	case "localsend/info":
		return p.localsendInfo(), nil
	case "folder/pick":
		return p.folderPick()
	case "channel/create":
		return p.channelCreate(params)
	case "channel/update":
		return p.channelUpdate(params)
	case "channel/delete":
		return p.channelDelete(params)
	case "channel/list":
		return p.channelList(), nil
	case "channel/join":
		return p.channelJoin(params)
	case "channel/leave":
		return p.channelLeave(params)
	case "channel/message":
		return p.channelMessage(params)
	case "channel/messages":
		return p.channelMessages(params)
	case "channel/add-file":
		return p.channelAddFile(params)
	case "channel/upload-file":
		return p.channelUploadFile(params)
	case "channel/read-file":
		return p.channelReadFile(params)
	case "channel/remove-file":
		return p.channelRemoveFile(params)
	case "channel/share-url":
		return p.channelShareURL(params)
	case "share/all-addresses":
		return p.shareAllAddresses(), nil
	case "system/open-url":
		var args struct {
			URL string `json:"url"`
		}
		_ = json.Unmarshal(params, &args)
		out, _ := p.systemOpenURL(args.URL)
		return out, nil
	case "system/reveal-file":
		return p.systemRevealFile(params)
	case "system/open-file":
		return p.systemOpenFile(params)
	case "peer/devices":
		return p.localsendDevices(), nil
	case "peer/send-file":
		return p.localsendSend(params)
	case "peer/ping":
		return p.localsendPing(params)
	case "industrial/ping":
		return map[string]any{"ok": true, "plugin": pluginID, "version": pluginVersion}, nil
	default:
		return nil, dbxpluginsdk.MethodNotFound(method)
	}
}

// emitEvent 推事件给前端
func (p *Plugin) emitEvent(method string, params any) {
	p.mu.RLock()
	em := p.emitter
	p.mu.RUnlock()
	if em != nil {
		_ = em.Event(method, params)
	}
}

// --- config methods ---
func (p *Plugin) configGet() any {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return map[string]any{"config": p.cfg}
}

func (p *Plugin) configSet(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var patch map[string]any
	if err := json.Unmarshal(params, &patch); err != nil {
		return nil, dbxpluginsdk.NewError(-32602, err.Error())
	}
	p.mu.Lock()
	if v, ok := patch["nickname"].(string); ok && v != "" {
		p.cfg.Nickname = v
	}
	if v, ok := patch["port"]; ok {
		switch n := v.(type) {
		case float64:
			p.cfg.Port = int(n)
		case int:
			p.cfg.Port = n
		}
	}
	if v, ok := patch["protocol"].(string); ok && v != "" {
		p.cfg.Protocol = v
	}
	if v, ok := patch["saveDir"].(string); ok && v != "" {
		p.cfg.SaveDir = v
	}
	if v, ok := patch["networkInterface"].(string); ok {
		p.cfg.NetworkIfSpec = v
	}
	if v, ok := patch["autoStart"].(bool); ok {
		p.cfg.AutoStart = v
	}
	// 刷新 UniqueID（因为网卡变了 MAC/IP 可能变）
	iface, _ := localsend.PickInterface(p.cfg.NetworkIfSpec)
	p.localIP = localsend.FirstIPv4(iface)
	p.localMAC = localsend.MACAddress(iface)
	p.cfg.IP = p.localIP
	p.cfg.MAC = p.localMAC
	p.cfg.UniqueID = localsend.DeviceID(p.localMAC, p.localIP)
	if p.cfg.SaveDir != "" {
		os.MkdirAll(p.cfg.SaveDir, 0o755)
	}
	if err := p.saveConfig(); err != nil {
		p.mu.Unlock()
		return map[string]any{"success": false, "message": err.Error()}, nil
	}
	cfg := p.cfg
	ip := p.localIP
	p.mu.Unlock()
	return map[string]any{"success": true, "config": cfg, "localIp": ip}, nil
}

func (p *Plugin) configStart() (any, *dbxpluginsdk.PluginError) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running {
		p.stopLocked()
	}
	if err := p.startLocked(); err != nil {
		return map[string]any{"success": false, "message": err.Error()}, nil
	}
	return map[string]any{"success": true, "status": p.statusSnapshot()}, nil
}

func (p *Plugin) configStop() (any, *dbxpluginsdk.PluginError) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopLocked()
	return map[string]any{"success": true, "status": p.statusSnapshot()}, nil
}

func (p *Plugin) configRestart() (any, *dbxpluginsdk.PluginError) {
	p.configStop()
	return p.configStart()
}

func (p *Plugin) configStatus() any { p.mu.RLock(); defer p.mu.RUnlock(); return p.statusSnapshot() }

func (p *Plugin) statusSnapshot() any {
	return map[string]any{
		"running":     p.running,
		"config":      p.cfg,
		"localIp":     p.localIP,
		"localMAC":    p.localMAC,
		"uniqueId":    p.cfg.UniqueID,
		"channels":    len(p.channels),
		"fingerprint": p.localFingerprint,
	}
}

func (p *Plugin) startLocked() error {
	iface, err := localsend.PickInterface(p.cfg.NetworkIfSpec)
	if err != nil {
		return fmt.Errorf("选网卡: %w", err)
	}
	p.localIP = localsend.FirstIPv4(iface)
	p.localMAC = localsend.MACAddress(iface)
	p.cfg.IP = p.localIP
	p.cfg.MAC = p.localMAC
	p.cfg.UniqueID = localsend.DeviceID(p.localMAC, p.localIP)

	disc := localsend.NewDiscovery(p.cfg.Nickname, p.localFingerprint, p.cfg.Port, p.cfg.Protocol, "desktop", "dbx-plugin", iface)
	// 事件回调：推设备上下线给前端
	disc.SetCallbacks(localsend.DiscoveryCallbacks{
		OnDeviceFound: func(dev localsend.DeviceInfo) {
			p.emitEvent("peer/device-found", map[string]any{
				"device": map[string]any{
					"fingerprint": dev.Fingerprint, "ip": dev.IP, "port": dev.Port,
					"name": dev.Name, "deviceModel": dev.DeviceModel, "deviceType": dev.DeviceType,
					"protocol": dev.Protocol, "download": dev.Download, "lastSeen": dev.LastSeen,
				},
			})
		},
		OnDeviceLost: func(fp string) {
			p.emitEvent("peer/device-lost", map[string]any{"fingerprint": fp})
		},
		OnDeviceUpdate: func(dev localsend.DeviceInfo) {
			p.emitEvent("peer/device-update", map[string]any{
				"device": map[string]any{
					"fingerprint": dev.Fingerprint, "ip": dev.IP, "port": dev.Port,
					"name": dev.Name, "deviceModel": dev.DeviceModel, "deviceType": dev.DeviceType,
					"protocol": dev.Protocol, "download": dev.Download, "lastSeen": dev.LastSeen,
				},
			})
		},
	})
	if err := disc.Start(); err != nil {
		return fmt.Errorf("启动发现: %w", err)
	}

	p.transferStore = localsend.NewTransferStore(p.cfg.SaveDir)

	srv := localsend.NewServer(p.cfg.SaveDir, localsend.ServerCallbacks{
		OnSendRequest: func(sender *localsend.SendRequest) *localsend.SendResponse {
			// 默认全部 accept（可扩展 UI 弹窗确认）
			return &localsend.SendResponse{Status: "accept"}
		},
		OnSessionDone: func(session *localsend.IncomingSession, err error) {
			if p.transferStore == nil {
				return
			}
			finishedAt := time.Now()
			startedAt := finishedAt // 入站不知道精确开始时间
			var totalSize int64
			for _, fm := range session.Files {
				totalSize += fm.Size
			}
			var status localsend.TransferStatus = localsend.TransferStatusSuccess
			var errMsg string
			if err != nil {
				status = localsend.TransferStatusFailed
				errMsg = err.Error()
			}
			log := &localsend.TransferLog{
				ID: localsend.GenSessionID(), Direction: localsend.TransferReceived,
				Status: status, SessionID: session.ID,
				PeerName: session.Sender.Alias, PeerFP: session.Sender.Fingerprint,
				Files: session.Files, TotalSize: totalSize, FileCount: len(session.Files),
				StartedAt: startedAt, FinishedAt: finishedAt,
				DurationMs: finishedAt.Sub(startedAt).Milliseconds(),
				ErrorMsg:   errMsg,
			}
			_ = p.transferStore.SaveLog(log)
			p.emitEvent("peer/transfer-received", map[string]any{"log": log})
		},
	}, "") // 空 bindIP → 监听 0.0.0.0，所有网卡都能访问
	srv.SetChannelInfo(p.cfg.Nickname, "", "main", "gateway")
	srv.SetInfoProvider(localsend.InfoProvider{
		Alias: p.cfg.Nickname, Version: "2.0", DeviceType: "desktop", DeviceModel: "dbx-plugin",
		Fingerprint: p.localFingerprint, Protocol: p.cfg.Protocol, Download: true,
	})
	actual, err := srv.Listen(p.cfg.Port)
	if err != nil {
		disc.Stop()
		return fmt.Errorf("启动 HTTP: %w", err)
	}

	// 注入频道 registry —— 让 server 的 /c/<channelID> 路由能查到频道
	srv.ChannelRegistry = func(id string) (*localsend.ChannelMeta, bool) {
		p.mu.RLock()
		defer p.mu.RUnlock()
		ch, ok := p.channels[id]
		if !ok {
			return nil, false
		}
		return &localsend.ChannelMeta{
			ID: ch.ID, Name: ch.Name, Visibility: ch.Visibility,
			PIN: ch.PIN, Port: p.cfg.Port,
		}, true
	}
	// 列出所有频道（用于 lobby /api/channels —— private 频道的 PIN 在 API 里由 server 端擦除）
	srv.ChannelListFn = func() []localsend.ChannelMeta {
		p.mu.RLock()
		defer p.mu.RUnlock()
		out := make([]localsend.ChannelMeta, 0, len(p.channels))
		for _, ch := range p.channels {
			out = append(out, localsend.ChannelMeta{
				ID: ch.ID, Name: ch.Name, Visibility: ch.Visibility,
				PIN: ch.PIN, Port: p.cfg.Port,
			})
		}
		return out
	}
	// 拿真实 Channel 对象（用于 chat endpoint 调 AppendMessage）
	srv.GetChannelFn = func(id string) (*localsend.Channel, bool) {
		p.mu.RLock()
		defer p.mu.RUnlock()
		ch, ok := p.channels[id]
		return ch, ok
	}

	// 端口变化同步回 discovery 和 server info
	p.cfg.Port = actual
	disc.UpdateLocalPort(actual)
	srv.SetInfoProvider(localsend.InfoProvider{
		Alias: p.cfg.Nickname, Version: "2.0", DeviceType: "desktop", DeviceModel: "dbx-plugin",
		Fingerprint: p.localFingerprint, Port: actual, Protocol: p.cfg.Protocol, Download: true,
	})

	// 所有频道（包括大厅）也要在新端口上重新 listen
	for _, ch := range p.channels {
		ch.LocalIP = p.localIP
		if ch.Port == 0 || ch.Port == defaultPort || ch.Port == actual {
			ch.Start(p.cfg.SaveDir)
		}
	}

	p.discovery = disc
	p.server = srv
	p.running = true
	return nil
}

func (p *Plugin) stopLocked() {
	p.running = false
	for _, ch := range p.channels {
		ch.StopServer()
	}
	if p.discovery != nil {
		p.discovery.Stop()
		p.discovery = nil
	}
	if p.server != nil {
		p.server.Close()
		p.server = nil
	}
}

func (p *Plugin) doStart() error { p.mu.Lock(); defer p.mu.Unlock(); return p.startLocked() }

// --- localsend ---

func (p *Plugin) localsendInterfaces() any {
	ifaces := localsend.ListInterfaces()
	out := make([]any, 0, len(ifaces))
	for _, i := range ifaces {
		out = append(out, map[string]any{
			"name": i.Name, "index": i.Index, "mtu": i.MTU, "flags": i.Flags,
			"mac": i.MAC, "ipv4": i.IPv4, "ipv6": i.IPv6, "isUp": i.IsUp, "isLoop": i.IsLoop,
		})
	}
	var recommended string
	for _, i := range ifaces {
		if i.IsUp && !i.IsLoop && len(i.IPv4) > 0 {
			recommended = i.Name
			break
		}
	}
	return map[string]any{"interfaces": out, "recommended": recommended}
}

func (p *Plugin) localsendDevices() any {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if !p.running || p.discovery == nil {
		return map[string]any{"devices": []any{}}
	}
	devs := p.discovery.Devices()
	out := make([]any, 0, len(devs))
	for _, d := range devs {
		out = append(out, map[string]any{
			"fingerprint": d.Fingerprint, "ip": d.IP, "port": d.Port,
			"name": d.Name, "deviceModel": d.DeviceModel, "deviceType": d.DeviceType,
			"protocol": d.Protocol, "download": d.Download, "lastSeen": d.LastSeen,
		})
	}
	return map[string]any{"devices": out}
}

func (p *Plugin) localsendScan() any {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.localsendDevices()
}

func (p *Plugin) localsendSend(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req struct {
		TargetIP   string                   `json:"targetIp"`
		TargetPort int                      `json:"targetPort"`
		TargetName string                   `json:"targetName"` // 可选，前端传个名字便于显示
		Files      []localsend.FileMetadata `json:"files"`
		Sources    []string                 `json:"sources"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, dbxpluginsdk.NewError(-32602, err.Error())
	}
	if req.TargetIP == "" {
		return nil, dbxpluginsdk.NewError(-32602, "缺少 targetIp")
	}
	if len(req.Files) == 0 || len(req.Sources) == 0 {
		return nil, dbxpluginsdk.NewError(-32602, "缺少 files / sources")
	}

	p.mu.RLock()
	localIP := p.localIP
	name := p.cfg.Nickname
	fp := p.localFingerprint
	myPort := p.cfg.Port
	store := p.transferStore
	p.mu.RUnlock()

	startedAt := time.Now()

	// 如果前端没给 port，尝试从 discovery 查找该 IP 对应的实际端口
	if req.TargetPort == 0 {
		p.mu.RLock()
		if p.discovery != nil {
			for _, d := range p.discovery.Devices() {
				if d.IP == req.TargetIP {
					req.TargetPort = d.Port
					if req.TargetName == "" {
						req.TargetName = d.Name
					}
					break
				}
			}
		}
		p.mu.RUnlock()
	}
	if req.TargetPort == 0 {
		req.TargetPort = defaultPort
	}

	// 匹配 discovery 拿到对方名称
	if req.TargetName == "" {
		p.mu.RLock()
		if p.discovery != nil {
			for _, d := range p.discovery.Devices() {
				if d.IP == req.TargetIP {
					req.TargetName = d.Name
					break
				}
			}
		}
		p.mu.RUnlock()
	}

	client := localsend.NewClient(defaultScheme, localIP)

	var totalSize int64
	for _, fm := range req.Files {
		totalSize += fm.Size
	}

	var (
		sessionID   string
		finalStatus localsend.TransferStatus = localsend.TransferStatusFailed
		errMsg      string
	)

	if _, err := client.Register(req.TargetIP, req.TargetPort, &localsend.RegisterRequest{
		Alias: name, Version: "2.0", DeviceType: "desktop", DeviceModel: "dbx-plugin",
		Fingerprint: fp, Port: myPort, Protocol: defaultScheme, Download: true,
	}); err != nil {
		errMsg = "注册失败: " + err.Error()
		p.saveSentLog(store, startedAt, req.TargetIP, req.TargetPort, req.TargetName,
			req.Files, totalSize, finalStatus, "", errMsg)
		return map[string]any{"success": false, "message": errMsg}, nil
	}
	sendResp, err := client.SendRequest(req.TargetIP, req.TargetPort, &localsend.SendRequest{
		Info: localsend.SendInfo{
			Alias: name, Version: "2.0", DeviceType: "desktop", DeviceModel: "dbx-plugin",
			Fingerprint: fp, Port: myPort, Protocol: defaultScheme, Download: true,
		},
		Files: req.Files, SkipPin: true,
	})
	if err != nil {
		errMsg = "send-request 失败: " + err.Error()
		p.saveSentLog(store, startedAt, req.TargetIP, req.TargetPort, req.TargetName,
			req.Files, totalSize, finalStatus, "", errMsg)
		return map[string]any{"success": false, "message": errMsg}, nil
	}
	sessionID = sendResp.SessionID
	if sendResp.Status == "decline" {
		finalStatus = localsend.TransferStatusDecline
		errMsg = "接收端拒绝了传输"
		p.saveSentLog(store, startedAt, req.TargetIP, req.TargetPort, req.TargetName,
			req.Files, totalSize, finalStatus, sessionID, errMsg)
		return map[string]any{"success": false, "message": errMsg}, nil
	}

	for i, fm := range req.Files {
		if i >= len(req.Sources) {
			break
		}
		f, err := os.Open(req.Sources[i])
		if err != nil {
			errMsg = "打开文件失败 " + err.Error()
			finalStatus = localsend.TransferStatusFailed
			p.saveSentLog(store, startedAt, req.TargetIP, req.TargetPort, req.TargetName,
				req.Files, totalSize, finalStatus, sessionID, errMsg)
			_ = client.Cancel(req.TargetIP, fmt.Sprintf("%d", req.TargetPort), sessionID)
			return map[string]any{"success": false, "message": errMsg}, nil
		}
		if err := client.Upload(req.TargetIP, req.TargetPort, sessionID, fm.ID, f, fm.Size); err != nil {
			f.Close()
			_ = client.Cancel(req.TargetIP, fmt.Sprintf("%d", req.TargetPort), sessionID)
			errMsg = "上传失败 " + err.Error()
			finalStatus = localsend.TransferStatusFailed
			p.saveSentLog(store, startedAt, req.TargetIP, req.TargetPort, req.TargetName,
				req.Files, totalSize, finalStatus, sessionID, errMsg)
			return map[string]any{"success": false, "message": errMsg}, nil
		}
		f.Close()
	}

	finalStatus = localsend.TransferStatusSuccess
	p.saveSentLog(store, startedAt, req.TargetIP, req.TargetPort, req.TargetName,
		req.Files, totalSize, finalStatus, sessionID, "")

	return map[string]any{"success": true, "message": fmt.Sprintf("已发送 %d 个文件到 %s:%d", len(req.Files), req.TargetIP, req.TargetPort)}, nil
}

// saveSentLog 写入一条出站传输记录（每个 server 独立存自己的）
func (p *Plugin) saveSentLog(store *localsend.TransferStore, startedAt time.Time,
	peerIP string, peerPort int, peerName string, files []localsend.FileMetadata,
	totalSize int64, status localsend.TransferStatus, sessionID, errMsg string) {
	if store == nil {
		return
	}
	finishedAt := time.Now()
	log := &localsend.TransferLog{
		ID: localsend.GenSessionID(), Direction: localsend.TransferSent,
		Status: status, SessionID: sessionID,
		PeerIP: peerIP, PeerPort: peerPort, PeerName: peerName,
		Files: files, TotalSize: totalSize, FileCount: len(files),
		StartedAt: startedAt, FinishedAt: finishedAt,
		DurationMs: finishedAt.Sub(startedAt).Milliseconds(),
		ErrorMsg:   errMsg,
	}
	_ = store.SaveLog(log)
	p.emitEvent("peer/transfer-sent", map[string]any{"log": log})
}

// localsendTransfers 返回传输历史
func (p *Plugin) localsendTransfers(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req struct {
		Direction string `json:"direction"`
	}
	if len(params) > 0 {
		_ = json.Unmarshal(params, &req)
	}

	p.mu.RLock()
	store := p.transferStore
	p.mu.RUnlock()
	if store == nil {
		return map[string]any{"transfers": []any{}}, nil
	}

	var logs []localsend.TransferLog
	switch localsend.TransferDirection(req.Direction) {
	case localsend.TransferSent, localsend.TransferReceived:
		logs = store.ListByDirection(localsend.TransferDirection(req.Direction))
	default:
		logs = store.List()
	}
	out := make([]any, 0, len(logs))
	for _, l := range logs {
		out = append(out, l)
	}
	return map[string]any{"transfers": out}, nil
}

func (p *Plugin) localsendPing(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req struct {
		IP   string `json:"ip"`
		Port int    `json:"port"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, dbxpluginsdk.NewError(-32602, err.Error())
	}
	if req.IP == "" {
		return nil, dbxpluginsdk.NewError(-32602, "缺少 ip")
	}
	if req.Port == 0 {
		req.Port = defaultPort
	}
	start := time.Now()
	err := localsend.NewClient(defaultScheme, p.localIP).Ping(req.IP, req.Port)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return map[string]any{"success": false, "latencyMs": latency, "message": err.Error()}, nil
	}
	return map[string]any{"success": true, "latencyMs": latency, "message": fmt.Sprintf("%s:%d reachable in %dms", req.IP, req.Port, latency)}, nil
}

func (p *Plugin) localsendInfo() any {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return map[string]any{
		"nickname": p.cfg.Nickname, "uniqueId": p.cfg.UniqueID, "mac": p.localMAC,
		"fingerprint": p.localFingerprint, "port": p.cfg.Port, "protocol": p.cfg.Protocol,
		"localIp": p.localIP, "saveDir": p.cfg.SaveDir, "running": p.running,
	}
}

// --- 跨平台目录选择 ---

func (p *Plugin) folderPick() (any, *dbxpluginsdk.PluginError) {
	dir, err := pickDirectory()
	if err != nil {
		return nil, dbxpluginsdk.NewError(-32602, "选择失败: "+err.Error())
	}

	if dir == "" {
		return map[string]any{"cancelled": true}, nil
	}
	return map[string]any{"cancelled": false, "path": dir}, nil
}

// --- 频道管理 ---

func (p *Plugin) channelCreate(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req struct {
		Name string `json:"name"`
		PIN  string `json:"pin"` // 可空，空则自动生成 6 位
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, dbxpluginsdk.NewError(-32602, err.Error())
	}
	if req.Name == "" {
		req.Name = "私有频道"
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	ch := localsend.NewChannel(
		req.Name, localsend.VisibilityPrivate,
		0, p.localIP, p.cfg.SaveDir,
		req.PIN, p.cfg.Nickname, p.localIP, p.cfg.UniqueID,
	)
	// 频道不再独立监听端口；这里始终记录 saveDir，保证元数据能落盘（重启后可恢复）
	if err := ch.Start(p.cfg.SaveDir); err != nil {
		return map[string]any{"success": false, "message": err.Error()}, nil
	}
	p.channels[ch.ID] = ch
	ch.SaveMeta()
	return ch.Snapshot(), nil
}

func (p *Plugin) channelUpdate(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		PIN        string `json:"pin"`
		Visibility string `json:"visibility"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, dbxpluginsdk.NewError(-32602, err.Error())
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	ch, ok := p.channels[req.ID]
	if !ok {
		return nil, dbxpluginsdk.NewError(-32602, "频道不存在")
	}
	// 公有频道强制保持公有
	if ch.Visibility == localsend.VisibilityPublic && req.Visibility == localsend.VisibilityPrivate {
		return map[string]any{"success": false, "message": "默认公有频道不能切换为私有"}, nil
	}
	ch.UpdateInfo(req.Name, req.PIN, req.Visibility)
	ch.SaveMeta()
	return map[string]any{"success": true, "channel": ch.Snapshot()}, nil
}

func (p *Plugin) channelDelete(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, dbxpluginsdk.NewError(-32602, err.Error())
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	ch, ok := p.channels[req.ID]
	if !ok {
		return nil, dbxpluginsdk.NewError(-32602, "频道不存在")
	}
	if ch.Visibility == localsend.VisibilityPublic && (ch.Name == "公有频道" || ch.Name == "大厅") {
		return map[string]any{"success": false, "message": "默认公有频道不能删除，只能清理成员/消息"}, nil
	}
	ch.Delete()
	delete(p.channels, req.ID)
	return map[string]any{"success": true}, nil
}

func (p *Plugin) channelList() any {
	p.mu.RLock()
	defer p.mu.RUnlock()

	type item struct {
		ch   *localsend.Channel
		snap map[string]any
	}
	var lobby *item
	var others []item
	for _, ch := range p.channels {
		snap := ch.Snapshot()
		it := item{ch: ch, snap: snap}
		if ch.Visibility == localsend.VisibilityPublic && (ch.Name == "公有频道" || ch.Name == "大厅") && lobby == nil {
			lobby = &it
		} else {
			others = append(others, it)
		}
	}
	// 按创建时间升序，频道顺序稳定（不会每次刷新乱跳）
	sort.Slice(others, func(i, j int) bool {
		return others[i].ch.CreatedAt.Before(others[j].ch.CreatedAt)
	})

	final := []any{}
	if lobby != nil {
		final = append(final, lobby.snap)
	}
	for _, o := range others {
		final = append(final, o.snap)
	}
	return map[string]any{"channels": final}
}

func (p *Plugin) channelJoin(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req struct {
		ChannelID string `json:"channelId"`
		Name      string `json:"name"`
		IP        string `json:"ip"`
		MemberID  string `json:"memberId"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, dbxpluginsdk.NewError(-32602, err.Error())
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	ch, ok := p.channels[req.ChannelID]
	if !ok {
		return nil, dbxpluginsdk.NewError(-32602, "频道不存在")
	}
	m := ch.AddMember(req.Name, req.IP, req.IP, req.MemberID) // deviceID 用 IP
	return map[string]any{"success": true, "member": m, "channel": ch.Snapshot()}, nil
}

func (p *Plugin) channelLeave(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req struct {
		ChannelID string `json:"channelId"`
		MemberID  string `json:"memberId"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, dbxpluginsdk.NewError(-32602, err.Error())
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	ch, ok := p.channels[req.ChannelID]
	if !ok {
		return nil, dbxpluginsdk.NewError(-32602, "频道不存在")
	}
	ch.RemoveMember(req.MemberID)
	return map[string]any{"success": true, "channel": ch.Snapshot()}, nil
}

func (p *Plugin) channelMessage(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req struct{ ChannelID, SenderID, Sender, Content, Type string }
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, dbxpluginsdk.NewError(-32602, err.Error())
	}
	if req.Content == "" {
		return nil, dbxpluginsdk.NewError(-32602, "消息内容不能为空")
	}
	if req.Type == "" {
		req.Type = "text"
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	ch, ok := p.channels[req.ChannelID]
	if !ok {
		return nil, dbxpluginsdk.NewError(-32602, "频道不存在")
	}
	m := ch.AppendMessageFrom(req.SenderID, p.cfg.UniqueID, req.Sender, req.Content, req.Type)
	return map[string]any{"success": true, "message": m}, nil
}

func (p *Plugin) channelMessages(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req struct {
		ChannelID string `json:"channelId"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, dbxpluginsdk.NewError(-32602, err.Error())
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	ch, ok := p.channels[req.ChannelID]
	if !ok {
		return nil, dbxpluginsdk.NewError(-32602, "频道不存在")
	}
	snap := ch.Snapshot()
	return map[string]any{"messages": snap["messages"]}, nil
}

func (p *Plugin) channelAddFile(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req struct {
		ChannelID string                 `json:"channelId"`
		File      localsend.FileMetadata `json:"file"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, dbxpluginsdk.NewError(-32602, err.Error())
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	ch, ok := p.channels[req.ChannelID]
	if !ok {
		return nil, dbxpluginsdk.NewError(-32602, "频道不存在")
	}
	ch.AddFile(req.File)
	ch.AppendMessage("", "系统", fmt.Sprintf("加入文件 %s", req.File.FileName), "system")
	return map[string]any{"success": true, "fileCount": len(ch.Files)}, nil
}

// channelUploadFile 接收前端通过 RPC 传上来的文件字节（base64），落盘到频道目录。
// 这样 DBX WebView 不需要跨 origin 访问本机 HTTP 端口。
func (p *Plugin) channelUploadFile(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req struct {
		ChannelID string `json:"channelId"`
		FileID    string `json:"fileId"`
		FileName  string `json:"fileName"`
		MimeType  string `json:"mimeType"`
		Data      string `json:"data"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, dbxpluginsdk.NewError(-32602, err.Error())
	}
	if req.Data == "" {
		return nil, dbxpluginsdk.NewError(-32602, "文件内容为空")
	}
	p.mu.RLock()
	ch, ok := p.channels[req.ChannelID]
	p.mu.RUnlock()
	if !ok {
		return nil, dbxpluginsdk.NewError(-32602, "频道不存在")
	}
	data, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		return nil, dbxpluginsdk.NewError(-32602, "文件内容解码失败: "+err.Error())
	}
	fm, err := ch.StoreFileData(req.FileID, req.FileName, req.MimeType, data)
	if err != nil {
		return nil, dbxpluginsdk.NewError(-32603, "保存文件失败: "+err.Error())
	}
	return map[string]any{"success": true, "file": fm}, nil
}

// channelReadFile 读取频道里的文件内容并 base64 返回，供主界面下载
func (p *Plugin) channelReadFile(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req struct {
		ChannelID string `json:"channelId"`
		FileID    string `json:"fileId"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, dbxpluginsdk.NewError(-32602, err.Error())
	}
	p.mu.RLock()
	ch, ok := p.channels[req.ChannelID]
	p.mu.RUnlock()
	if !ok {
		return nil, dbxpluginsdk.NewError(-32602, "频道不存在")
	}
	fm, ok := ch.FileByID(req.FileID)
	if !ok {
		return nil, dbxpluginsdk.NewError(-32602, "文件不存在")
	}
	data, err := os.ReadFile(fm.LocalPath)
	if err != nil {
		return nil, dbxpluginsdk.NewError(-32603, "读取文件失败: "+err.Error())
	}
	return map[string]any{
		"success":  true,
		"fileName": fm.FileName,
		"mimeType": fm.MIME,
		"data":     base64.StdEncoding.EncodeToString(data),
	}, nil
}

func (p *Plugin) channelRemoveFile(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req struct {
		ChannelID string `json:"channelId"`
		FileID    string `json:"fileId"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, dbxpluginsdk.NewError(-32602, err.Error())
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	ch, ok := p.channels[req.ChannelID]
	if !ok {
		return nil, dbxpluginsdk.NewError(-32602, "频道不存在")
	}
	ch.RemoveFile(req.FileID)
	return map[string]any{"success": true, "fileCount": len(ch.Files)}, nil
}

func (p *Plugin) channelShareURL(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, dbxpluginsdk.NewError(-32602, err.Error())
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	ch, ok := p.channels[req.ID]
	if !ok {
		return nil, dbxpluginsdk.NewError(-32602, "频道不存在")
	}
	snap := ch.Snapshot()
	snap["addresses"] = channelAddresses(ch, p.cfg.Port, p.cfg.Protocol)
	return snap, nil
}

// systemOpenURL 用系统默认浏览器打开 URL（Windows: rundll32 url.dll,FileProtocolHandler）
func (p *Plugin) systemOpenURL(url string) (any, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return nil, fmt.Errorf("缺少 url 参数")
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	} else if runtime.GOOS == "darwin" {
		cmd = exec.Command("open", url)
	} else {
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("打开浏览器: %w", err)
	}
	return map[string]any{"ok": true}, nil
}

// systemRevealFile 用资源管理器打开文件所在目录（有文件时定位并选中该文件）
func (p *Plugin) systemRevealFile(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req struct {
		ChannelID string `json:"channelId"`
		FileID    string `json:"fileId"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, dbxpluginsdk.NewError(-32602, err.Error())
	}
	p.mu.RLock()
	ch, ok := p.channels[req.ChannelID]
	p.mu.RUnlock()
	if !ok {
		return nil, dbxpluginsdk.NewError(-32602, "频道不存在")
	}
	dir := ch.FileDir()
	if dir == "" {
		return nil, dbxpluginsdk.NewError(-32603, "未配置保存目录")
	}
	os.MkdirAll(dir, 0o755)

	target := ""
	if req.FileID != "" {
		if fm, ok := ch.FileByID(req.FileID); ok && fm.LocalPath != "" {
			if _, err := os.Stat(fm.LocalPath); err == nil {
				target = fm.LocalPath
			}
		}
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		if target != "" {
			cmd = exec.Command("explorer.exe", "/select,"+target)
		} else {
			cmd = exec.Command("explorer.exe", dir)
		}
	case "darwin":
		if target != "" {
			cmd = exec.Command("open", "-R", target)
		} else {
			cmd = exec.Command("open", dir)
		}
	default:
		cmd = exec.Command("xdg-open", dir)
	}
	if err := cmd.Start(); err != nil {
		return nil, dbxpluginsdk.NewError(-32603, "打开目录失败: "+err.Error())
	}
	return map[string]any{"ok": true, "dir": dir, "file": target}, nil
}

// systemOpenFile 用系统默认程序直接打开频道里的文件
func (p *Plugin) systemOpenFile(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req struct {
		ChannelID string `json:"channelId"`
		FileID    string `json:"fileId"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, dbxpluginsdk.NewError(-32602, err.Error())
	}
	p.mu.RLock()
	ch, ok := p.channels[req.ChannelID]
	p.mu.RUnlock()
	if !ok {
		return nil, dbxpluginsdk.NewError(-32602, "频道不存在")
	}
	fm, ok := ch.FileByID(req.FileID)
	if !ok || fm.LocalPath == "" {
		return nil, dbxpluginsdk.NewError(-32602, "文件不存在")
	}
	if _, err := os.Stat(fm.LocalPath); err != nil {
		return nil, dbxpluginsdk.NewError(-32603, "文件不存在或已被移动")
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", fm.LocalPath)
	case "darwin":
		cmd = exec.Command("open", fm.LocalPath)
	default:
		cmd = exec.Command("xdg-open", fm.LocalPath)
	}
	if err := cmd.Start(); err != nil {
		return nil, dbxpluginsdk.NewError(-32603, "打开文件失败: "+err.Error())
	}
	return map[string]any{"ok": true, "path": fm.LocalPath}, nil
}

// shareAllAddresses 返回所有频道在所有可用 IP 上的访问地址
func (p *Plugin) shareAllAddresses() any {
	p.mu.RLock()
	defer p.mu.RUnlock()
	type chItem struct {
		ID         string         `json:"id"`
		Name       string         `json:"name"`
		Visibility string         `json:"visibility"`
		PIN        string         `json:"pin"`
		Port       int            `json:"port"`
		Addresses  []addressEntry `json:"addresses"`
	}
	out := make([]chItem, 0, len(p.channels))
	for _, ch := range p.channels {
		out = append(out, chItem{
			ID: ch.ID, Name: ch.Name, Visibility: ch.Visibility,
			PIN: ch.PIN, Port: p.cfg.Port,
			Addresses: channelAddresses(ch, p.cfg.Port, p.cfg.Protocol),
		})
	}
	return map[string]any{
		"localIp":        p.localIP,
		"interfaces":     localsend.ListInterfaces(),
		"channels":       out,
		"nickname":       p.cfg.Nickname,
		"defaultSaveDir": p.cfg.SaveDir,
	}
}

// addressEntry 一个访问地址
type addressEntry struct {
	IP   string `json:"ip"`
	Port int    `json:"port"`
	URL  string `json:"url"`
	Type string `json:"type"` // lan / wifi / ethernet / tunnel / loopback
}

// channelAddresses 收集一个频道的所有可达地址
func channelAddresses(ch *localsend.Channel, port int, scheme string) []addressEntry {
	ips := collectReachableIPs()
	out := make([]addressEntry, 0, len(ips))
	for _, ip := range ips {
		url := fmt.Sprintf("%s://%s:%d", scheme, ip, port)
		out = append(out, addressEntry{IP: ip, Port: port, URL: url, Type: ipType(ip)})
	}
	return out
}

func collectReachableIPs() []string {
	ifaces := localsend.ListInterfaces()
	seen := map[string]bool{}
	var out []string
	for _, iface := range ifaces {
		if iface.IsLoop {
			continue
		}
		if !iface.IsUp {
			continue
		}
		for _, ip := range iface.IPv4 {
			// 过滤掉 link-local (169.254.x.x) 和 multicast (224.x.x.x)
			if strings.HasPrefix(ip, "169.254.") || strings.HasPrefix(ip, "224.") {
				continue
			}
			if strings.HasPrefix(ip, "0.") || strings.HasPrefix(ip, "127.") {
				continue
			}
			if seen[ip] {
				continue
			}
			seen[ip] = true
			out = append(out, ip)
		}
	}
	// 兜底：如果没找到（虚拟机/容器环境），至少返回 127.0.0.1
	if len(out) == 0 {
		out = append(out, "127.0.0.1")
	}
	return out
}

func ipType(ip string) string {
	if strings.HasPrefix(ip, "192.168.") || strings.HasPrefix(ip, "10.") {
		return "lan"
	}
	if strings.HasPrefix(ip, "172.") {
		// 172.16.0.0 - 172.31.255.255
		rest := strings.TrimPrefix(ip, "172.")
		if dot := strings.Index(rest, "."); dot > 0 {
			second := rest[:dot]
			switch second {
			case "16", "17", "18", "19", "20", "21", "22", "23", "24", "25", "26", "27", "28", "29", "30", "31":
				return "lan"
			}
		}
	}
	if strings.HasPrefix(ip, "127.") {
		return "loopback"
	}
	return "other"
}

var _ dbxpluginsdk.Handler = (*Plugin)(nil)
var _ io.Reader = nil
