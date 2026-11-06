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

	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"
	"github.com/bobocha214/dbx-plugin-localsend/localsend"
)

const (
	pluginID      = "io.github.yuzhi.localsend"
	pluginVersion = "0.1.0"
	defaultPort   = localsend.DefaultPort
	defaultScheme = "http"
)

// Config 持久化到 config.json
type Config struct {
	Nickname      string `json:"nickname"`      // 自定义昵称，给人看的
	UniqueID      string `json:"uniqueId"`      // 机器唯一 ID（MAC@IP），自动生成
	MAC           string `json:"mac"`           // MAC 地址本身，调试用
	IP            string `json:"ip"`            // 当前选定网卡的本机 IP
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

	exe, _ := os.Executable()
	plugin.cfgPath = filepath.Join(filepath.Dir(exe), "config.json")

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
	// 默认大厅（如果恢复里没有大厅就建一个）
	lobbyExists := false
	for _, ch := range plugin.channels {
		if ch.Name == "大厅" && ch.Visibility == localsend.VisibilityPublic {
			lobbyExists = true
			break
		}
	}
	if !lobbyExists {
		lobby := localsend.DefaultLobby(plugin.localIP, plugin.cfg.SaveDir, plugin.cfg.Nickname, plugin.cfg.UniqueID)
		plugin.channels[lobby.ID] = lobby
	}

	if plugin.cfg.AutoStart {
		if err := plugin.doStart(); err != nil {
			fmt.Fprintf(os.Stderr, "[%s] auto start: %v\n", pluginID, err)
		}
	}

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
		if os.IsNotExist(err) { return nil }
		return err
	}
	var loaded Config
	if err := json.Unmarshal(data, &loaded); err != nil { return err }
	merged := defaultConfig()
	if loaded.Nickname != "" { merged.Nickname = loaded.Nickname }
	if loaded.Port != 0 { merged.Port = loaded.Port }
	if loaded.Protocol != "" { merged.Protocol = loaded.Protocol }
	if loaded.SaveDir != "" { merged.SaveDir = loaded.SaveDir }
	if loaded.NetworkIfSpec != "" { merged.NetworkIfSpec = loaded.NetworkIfSpec }
	merged.AutoStart = loaded.AutoStart
	p.cfg = merged
	return nil
}

func (p *Plugin) saveConfig() error {
	data, err := json.MarshalIndent(p.cfg, "", "  ")
	if err != nil { return err }
	return os.WriteFile(p.cfgPath, data, 0o644)
}

// --- Handler ---

func (p *Plugin) Handle(
	_ dbxpluginsdk.RequestContext, method string, params json.RawMessage, _ *dbxpluginsdk.Emitter,
) (any, *dbxpluginsdk.PluginError) {
	switch method {
	case "config/get": return p.configGet(), nil
	case "config/set": return p.configSet(params)
	case "config/start": return p.configStart()
	case "config/stop": return p.configStop()
	case "config/restart": return p.configRestart()
	case "config/status": return p.configStatus(), nil
	case "localsend/interfaces": return p.localsendInterfaces(), nil
	case "localsend/devices": return p.localsendDevices(), nil
	case "localsend/scan": return p.localsendScan(), nil
	case "localsend/send": return p.localsendSend(params)
	case "localsend/ping": return p.localsendPing(params)
	case "localsend/info": return p.localsendInfo(), nil
	case "folder/pick": return p.folderPick()
	case "channel/create": return p.channelCreate(params)
	case "channel/update": return p.channelUpdate(params)
	case "channel/delete": return p.channelDelete(params)
	case "channel/list": return p.channelList(), nil
	case "channel/join": return p.channelJoin(params)
	case "channel/leave": return p.channelLeave(params)
	case "channel/message": return p.channelMessage(params)
	case "channel/messages": return p.channelMessages(params)
	case "channel/add-file": return p.channelAddFile(params)
	case "channel/remove-file": return p.channelRemoveFile(params)
	case "channel/share-url": return p.channelShareURL(params)
	case "industrial/ping": return map[string]any{"ok": true, "plugin": pluginID, "version": pluginVersion}, nil
	default: return nil, dbxpluginsdk.MethodNotFound(method)
	}
}

// --- config methods ---

func (p *Plugin) configGet() any { p.mu.RLock(); defer p.mu.RUnlock(); return map[string]any{"config": p.cfg} }

func (p *Plugin) configSet(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var patch map[string]any
	if err := json.Unmarshal(params, &patch); err != nil { return nil, dbxpluginsdk.NewError(-32602, err.Error()) }
	p.mu.Lock()
	if v, ok := patch["nickname"].(string); ok && v != "" { p.cfg.Nickname = v }
	if v, ok := patch["port"]; ok {
		switch n := v.(type) { case float64: p.cfg.Port = int(n); case int: p.cfg.Port = n }
	}
	if v, ok := patch["protocol"].(string); ok && v != "" { p.cfg.Protocol = v }
	if v, ok := patch["saveDir"].(string); ok && v != "" { p.cfg.SaveDir = v }
	if v, ok := patch["networkInterface"].(string); ok { p.cfg.NetworkIfSpec = v }
	if v, ok := patch["autoStart"].(bool); ok { p.cfg.AutoStart = v }
	// 刷新 UniqueID（因为网卡变了 MAC/IP 可能变）
	iface, _ := localsend.PickInterface(p.cfg.NetworkIfSpec)
	p.localIP = localsend.FirstIPv4(iface)
	p.localMAC = localsend.MACAddress(iface)
	p.cfg.IP = p.localIP; p.cfg.MAC = p.localMAC; p.cfg.UniqueID = localsend.DeviceID(p.localMAC, p.localIP)
	if p.cfg.SaveDir != "" { os.MkdirAll(p.cfg.SaveDir, 0o755) }
	if err := p.saveConfig(); err != nil { p.mu.Unlock(); return map[string]any{"success": false, "message": err.Error()}, nil }
	cfg := p.cfg; ip := p.localIP; p.mu.Unlock()
	return map[string]any{"success": true, "config": cfg, "localIp": ip}, nil
}

func (p *Plugin) configStart() (any, *dbxpluginsdk.PluginError) {
	p.mu.Lock(); defer p.mu.Unlock()
	if p.running { p.stopLocked() }
	if err := p.startLocked(); err != nil {
		return map[string]any{"success": false, "message": err.Error()}, nil
	}
	return map[string]any{"success": true, "status": p.statusSnapshot()}, nil
}

func (p *Plugin) configStop() (any, *dbxpluginsdk.PluginError) {
	p.mu.Lock(); defer p.mu.Unlock(); p.stopLocked()
	return map[string]any{"success": true, "status": p.statusSnapshot()}, nil
}

func (p *Plugin) configRestart() (any, *dbxpluginsdk.PluginError) { p.configStop(); return p.configStart() }

func (p *Plugin) configStatus() any { p.mu.RLock(); defer p.mu.RUnlock(); return p.statusSnapshot() }

func (p *Plugin) statusSnapshot() any {
	return map[string]any{
		"running":      p.running,
		"config":       p.cfg,
		"localIp":      p.localIP,
		"localMAC":     p.localMAC,
		"uniqueId":     p.cfg.UniqueID,
		"channels":     len(p.channels),
		"fingerprint":  p.localFingerprint,
	}
}

func (p *Plugin) startLocked() error {
	iface, err := localsend.PickInterface(p.cfg.NetworkIfSpec)
	if err != nil { return fmt.Errorf("选网卡: %w", err) }
	p.localIP = localsend.FirstIPv4(iface)
	p.localMAC = localsend.MACAddress(iface)
	p.cfg.IP = p.localIP; p.cfg.MAC = p.localMAC; p.cfg.UniqueID = localsend.DeviceID(p.localMAC, p.localIP)

	disc := localsend.NewDiscovery(p.cfg.Nickname, p.localFingerprint, p.cfg.Port, p.cfg.Protocol, "desktop", "dbx-plugin", iface)
	if err := disc.Start(); err != nil { return fmt.Errorf("启动发现: %w", err) }

	srv := localsend.NewServer(p.cfg.SaveDir, localsend.ServerCallbacks{}, p.localIP)
	srv.SetChannelInfo(p.cfg.Nickname, "", "main", "gateway")
	actual, err := srv.Listen(p.cfg.Port)
	if err != nil { disc.Stop(); return fmt.Errorf("启动 HTTP: %w", err) }

	// 所有频道（包括大厅）也要在新端口上重新 listen
	for _, ch := range p.channels {
		ch.LocalIP = p.localIP
		if ch.Port == 0 || ch.Port == defaultPort || ch.Port == p.cfg.Port {
			ch.Start(p.cfg.SaveDir)
		}
	}

	p.discovery = disc; p.server = srv; p.cfg.Port = actual; p.running = true
	return nil
}

func (p *Plugin) stopLocked() {
	p.running = false
	for _, ch := range p.channels { ch.StopServer() }
	if p.discovery != nil { p.discovery.Stop(); p.discovery = nil }
	if p.server != nil { p.server.Close(); p.server = nil }
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
	for _, i := range ifaces { if i.IsUp && !i.IsLoop && len(i.IPv4) > 0 { recommended = i.Name; break } }
	return map[string]any{"interfaces": out, "recommended": recommended}
}

func (p *Plugin) localsendDevices() any {
	p.mu.RLock(); defer p.mu.RUnlock()
	if !p.running || p.discovery == nil { return map[string]any{"devices": []any{}} }
	devs := p.discovery.Devices()
	out := make([]any, 0, len(devs))
	for _, d := range devs {
		out = append(out, map[string]any{
			"fingerprint": d.Fingerprint, "ip": d.IP, "port": d.Port,
			"name": d.Name, "deviceModel": d.DeviceModel, "deviceType": d.DeviceType,
			"protocol": d.Protocol, "download": d.Download,
		})
	}
	return map[string]any{"devices": out}
}

func (p *Plugin) localsendScan() any { time.Sleep(2 * time.Second); return p.localsendDevices() }

func (p *Plugin) localsendSend(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req struct {
		TargetIP   string                   `json:"targetIp"`
		TargetPort int                      `json:"targetPort"`
		Files      []localsend.FileMetadata `json:"files"`
		Sources    []string                 `json:"sources"`
	}
	if err := json.Unmarshal(params, &req); err != nil { return nil, dbxpluginsdk.NewError(-32602, err.Error()) }
	if req.TargetIP == "" { return nil, dbxpluginsdk.NewError(-32602, "缺少 targetIp") }
	if req.TargetPort == 0 { req.TargetPort = defaultPort }
	if len(req.Files) == 0 || len(req.Sources) == 0 { return nil, dbxpluginsdk.NewError(-32602, "缺少 files / sources") }

	p.mu.RLock(); localIP := p.localIP; name := p.cfg.Nickname; fp := p.localFingerprint; p.mu.RUnlock()
	client := localsend.NewClient(defaultScheme, localIP)

	if _, err := client.Register(req.TargetIP, req.TargetPort, &localsend.RegisterRequest{
		Alias: name, Version: "2.0", DeviceType: "desktop", DeviceModel: "dbx-plugin",
		Fingerprint: fp, Port: defaultPort, Protocol: defaultScheme, Download: true,
	}); err != nil {
		return map[string]any{"success": false, "message": "注册失败: " + err.Error()}, nil
	}
	sendResp, err := client.SendRequest(req.TargetIP, req.TargetPort, &localsend.SendRequest{
		Info: localsend.SendInfo{
			Alias: name, Version: "2.0", DeviceType: "desktop", DeviceModel: "dbx-plugin",
			Fingerprint: fp, Port: defaultPort, Protocol: defaultScheme, Download: true,
		},
		Files: req.Files, SkipPin: true,
	})
	if err != nil { return map[string]any{"success": false, "message": "send-request 失败: " + err.Error()}, nil }
	if sendResp.Status == "decline" { return map[string]any{"success": false, "message": "接收端拒绝了传输"}, nil }

	for i, fm := range req.Files {
		if i >= len(req.Sources) { break }
		f, err := os.Open(req.Sources[i])
		if err != nil { client.Cancel(req.TargetIP, fmt.Sprintf("%d", req.TargetPort), sendResp.SessionID); return map[string]any{"success": false, "message": "打开文件失败 " + err.Error()}, nil }
		if err := client.Upload(req.TargetIP, req.TargetPort, sendResp.SessionID, fm.ID, f, fm.Size); err != nil {
			f.Close(); client.Cancel(req.TargetIP, fmt.Sprintf("%d", req.TargetPort), sendResp.SessionID)
			return map[string]any{"success": false, "message": "上传失败 " + err.Error()}, nil
		}
		f.Close()
	}
	return map[string]any{"success": true, "message": fmt.Sprintf("已发送 %d 个文件", len(req.Files))}, nil
}

func (p *Plugin) localsendPing(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req struct{ IP string `json:"ip"`; Port int `json:"port"` }
	if err := json.Unmarshal(params, &req); err != nil { return nil, dbxpluginsdk.NewError(-32602, err.Error()) }
	if req.IP == "" { return nil, dbxpluginsdk.NewError(-32602, "缺少 ip") }
	if req.Port == 0 { req.Port = defaultPort }
	start := time.Now()
	err := localsend.NewClient(defaultScheme, p.localIP).Ping(req.IP, req.Port)
	latency := time.Since(start).Milliseconds()
	if err != nil { return map[string]any{"success": false, "latencyMs": latency, "message": err.Error()}, nil }
	return map[string]any{"success": true, "latencyMs": latency, "message": fmt.Sprintf("%s:%d reachable in %dms", req.IP, req.Port, latency)}, nil
}

func (p *Plugin) localsendInfo() any {
	p.mu.RLock(); defer p.mu.RUnlock()
	return map[string]any{
		"nickname": p.cfg.Nickname, "uniqueId": p.cfg.UniqueID, "mac": p.localMAC,
		"fingerprint": p.localFingerprint, "port": p.cfg.Port, "protocol": p.cfg.Protocol,
		"localIp": p.localIP, "saveDir": p.cfg.SaveDir, "running": p.running,
	}
}

// --- 目录选择（Windows 弹 PowerShell FolderBrowserDialog）---

func (p *Plugin) folderPick() (any, *dbxpluginsdk.PluginError) {
	if runtime.GOOS != "windows" {
		return nil, dbxpluginsdk.NewError(-32602, "当前只支持 Windows 选择目录")
	}
	// PowerShell 弹 FolderBrowserDialog，输出选中路径
	script := `Add-Type -AssemblyName System.Windows.Forms; $d = New-Object System.Windows.Forms.FolderBrowserDialog; $d.Description = '选择接收文件夹'; if ($d.ShowDialog() -eq 'OK') { Write-Output $d.SelectedPath }`
	cmd := exec.Command("powershell", "-NoProfile", "-Command", script)
	out, err := cmd.Output()
	if err != nil { return nil, dbxpluginsdk.NewError(-32602, "选择失败: "+err.Error()) }
	dir := strings.TrimSpace(string(out))
	if dir == "" { return map[string]any{"cancelled": true}, nil }
	return map[string]any{"cancelled": false, "path": dir}, nil
}

// --- 频道管理 ---

func (p *Plugin) channelCreate(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req struct {
		Name string `json:"name"`
		PIN  string `json:"pin"` // 可空，空则自动生成 6 位
	}
	if err := json.Unmarshal(params, &req); err != nil { return nil, dbxpluginsdk.NewError(-32602, err.Error()) }
	if req.Name == "" { req.Name = "私有频道" }
	p.mu.Lock(); defer p.mu.Unlock()

	ch := localsend.NewChannel(
		req.Name, localsend.VisibilityPrivate,
		0, p.localIP, p.cfg.SaveDir,
		req.PIN, p.cfg.Nickname, p.localIP, p.cfg.UniqueID,
	)
	if p.running {
		if err := ch.Start(p.cfg.SaveDir); err != nil {
			return map[string]any{"success": false, "message": err.Error()}, nil
		}
	}
	p.channels[ch.ID] = ch
	return ch.Snapshot(), nil
}

func (p *Plugin) channelUpdate(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		PIN        string `json:"pin"`
		Visibility string `json:"visibility"`
	}
	if err := json.Unmarshal(params, &req); err != nil { return nil, dbxpluginsdk.NewError(-32602, err.Error()) }
	p.mu.Lock(); defer p.mu.Unlock()
	ch, ok := p.channels[req.ID]
	if !ok { return nil, dbxpluginsdk.NewError(-32602, "频道不存在") }
	// 大厅强制公有
	if ch.Name == "大厅" && req.Visibility == localsend.VisibilityPrivate {
		return map[string]any{"success": false, "message": "大厅必须是公有频道"}, nil
	}
	ch.UpdateInfo(req.Name, req.PIN, req.Visibility)
	return map[string]any{"success": true, "channel": ch.Snapshot()}, nil
}

func (p *Plugin) channelDelete(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req struct{ ID string `json:"id"` }
	if err := json.Unmarshal(params, &req); err != nil { return nil, dbxpluginsdk.NewError(-32602, err.Error()) }
	p.mu.Lock(); defer p.mu.Unlock()
	ch, ok := p.channels[req.ID]
	if !ok { return nil, dbxpluginsdk.NewError(-32602, "频道不存在") }
	if ch.Visibility == localsend.VisibilityPublic && ch.Name == "大厅" {
		return map[string]any{"success": false, "message": "默认大厅不能删除，只能清理成员/消息"}, nil
	}
	ch.Delete(); delete(p.channels, req.ID)
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
		if ch.Name == "大厅" && ch.Visibility == localsend.VisibilityPublic && lobby == nil {
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
	if lobby != nil { final = append(final, lobby.snap) }
	for _, o := range others { final = append(final, o.snap) }
	return map[string]any{"channels": final}
}

func (p *Plugin) channelJoin(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req struct {
		ChannelID string `json:"channelId"`
		Name      string `json:"name"`
		IP        string `json:"ip"`
		MemberID  string `json:"memberId"`
	}
	if err := json.Unmarshal(params, &req); err != nil { return nil, dbxpluginsdk.NewError(-32602, err.Error()) }
	p.mu.Lock(); defer p.mu.Unlock()
	ch, ok := p.channels[req.ChannelID]
	if !ok { return nil, dbxpluginsdk.NewError(-32602, "频道不存在") }
	m := ch.AddMember(req.Name, req.IP, req.IP, req.MemberID) // deviceID 用 IP
	return map[string]any{"success": true, "member": m, "channel": ch.Snapshot()}, nil
}

func (p *Plugin) channelLeave(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req struct {
		ChannelID string `json:"channelId"`
		MemberID  string `json:"memberId"`
	}
	if err := json.Unmarshal(params, &req); err != nil { return nil, dbxpluginsdk.NewError(-32602, err.Error()) }
	p.mu.Lock(); defer p.mu.Unlock()
	ch, ok := p.channels[req.ChannelID]
	if !ok { return nil, dbxpluginsdk.NewError(-32602, "频道不存在") }
	ch.RemoveMember(req.MemberID)
	return map[string]any{"success": true, "channel": ch.Snapshot()}, nil
}

func (p *Plugin) channelMessage(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req struct{ ChannelID, SenderID, Sender, Content, Type string }
	if err := json.Unmarshal(params, &req); err != nil { return nil, dbxpluginsdk.NewError(-32602, err.Error()) }
	if req.Content == "" { return nil, dbxpluginsdk.NewError(-32602, "消息内容不能为空") }
	if req.Type == "" { req.Type = "text" }
	p.mu.Lock(); defer p.mu.Unlock()
	ch, ok := p.channels[req.ChannelID]
	if !ok { return nil, dbxpluginsdk.NewError(-32602, "频道不存在") }
	m := ch.AppendMessage(req.SenderID, req.Sender, req.Content, req.Type)
	return map[string]any{"success": true, "message": m}, nil
}

func (p *Plugin) channelMessages(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req struct{ ChannelID string `json:"channelId"` }
	if err := json.Unmarshal(params, &req); err != nil { return nil, dbxpluginsdk.NewError(-32602, err.Error()) }
	p.mu.RLock(); defer p.mu.RUnlock()
	ch, ok := p.channels[req.ChannelID]
	if !ok { return nil, dbxpluginsdk.NewError(-32602, "频道不存在") }
	snap := ch.Snapshot()
	return map[string]any{"messages": snap["messages"]}, nil
}

func (p *Plugin) channelAddFile(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req struct{ ChannelID string `json:"channelId"`; File localsend.FileMetadata `json:"file"` }
	if err := json.Unmarshal(params, &req); err != nil { return nil, dbxpluginsdk.NewError(-32602, err.Error()) }
	p.mu.Lock(); defer p.mu.Unlock()
	ch, ok := p.channels[req.ChannelID]
	if !ok { return nil, dbxpluginsdk.NewError(-32602, "频道不存在") }
	ch.AddFile(req.File)
	ch.AppendMessage("", "系统", fmt.Sprintf("加入文件 %s", req.File.FileName), "system")
	return map[string]any{"success": true, "fileCount": len(ch.Files)}, nil
}

func (p *Plugin) channelRemoveFile(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req struct {
		ChannelID string `json:"channelId"`
		FileID    string `json:"fileId"`
	}
	if err := json.Unmarshal(params, &req); err != nil { return nil, dbxpluginsdk.NewError(-32602, err.Error()) }
	p.mu.Lock(); defer p.mu.Unlock()
	ch, ok := p.channels[req.ChannelID]
	if !ok { return nil, dbxpluginsdk.NewError(-32602, "频道不存在") }
	ch.RemoveFile(req.FileID)
	return map[string]any{"success": true, "fileCount": len(ch.Files)}, nil
}

func (p *Plugin) channelShareURL(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req struct{ ID string `json:"id"` }
	if err := json.Unmarshal(params, &req); err != nil { return nil, dbxpluginsdk.NewError(-32602, err.Error()) }
	p.mu.RLock(); defer p.mu.RUnlock()
	ch, ok := p.channels[req.ID]
	if !ok { return nil, dbxpluginsdk.NewError(-32602, "频道不存在") }
	snap := ch.Snapshot(); snap["url"] = ch.ShareURL(p.cfg.Protocol); return snap, nil
}

var _ dbxpluginsdk.Handler = (*Plugin)(nil)
var _ io.Reader = nil
