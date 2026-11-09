// UDP 多播发现：发送 announce + 监听 announce
package localsend

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"
)

// DeviceTtl 设备在没有新 announce 到达多久后标记离线
const DeviceTtl = 10 * time.Second

// DeviceInfo 增强版，带最近活跃时间
type DeviceInfo struct {
	Fingerprint string    `json:"fingerprint"`
	IP          string    `json:"ip"`
	Port        int       `json:"port"`
	Name        string    `json:"name"`
	DeviceModel string    `json:"deviceModel,omitempty"`
	DeviceType  string    `json:"deviceType"`
	Protocol    string    `json:"protocol"`
	Download    bool      `json:"download"`
	LastSeen    time.Time `json:"lastSeen"`
}

// DiscoveryCallbacks 发现事件回调
type DiscoveryCallbacks struct {
	OnDeviceFound func(dev DeviceInfo)      // 新设备出现
	OnDeviceLost  func(fingerprint string) // 设备离线
	OnDeviceUpdate func(dev DeviceInfo)     // 设备信息更新
}

// Discovery 负责本地局域网内 LocalSend 设备的发现
type Discovery struct {
	mu      sync.RWMutex
	devices map[string]*DeviceInfo

	localName        string
	localFingerprint string
	localPort        int
	localProtocol    string
	localDevType     string
	localDevModel    string

	iface *net.Interface // nil = 系统默认

	conn      *net.UDPConn
	stop      chan struct{}
	wg        sync.WaitGroup
	callbacks DiscoveryCallbacks
}

func NewDiscovery(localName, localFingerprint string, localPort int, protocol, devType, devModel string, iface *net.Interface) *Discovery {
	return &Discovery{
		devices:          make(map[string]*DeviceInfo),
		localName:        localName,
		localFingerprint: localFingerprint,
		localPort:        localPort,
		localProtocol:    protocol,
		localDevType:     devType,
		localDevModel:    devModel,
		iface:            iface,
		stop:             make(chan struct{}),
	}
}

// SetCallbacks 设置事件回调
func (d *Discovery) SetCallbacks(cb DiscoveryCallbacks) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.callbacks = cb
}

// UpdateLocalPort 本机实际端口变化时调用（例如端口冲突自动换）
func (d *Discovery) UpdateLocalPort(port int) {
	d.mu.Lock()
	d.localPort = port
	d.mu.Unlock()
}

// Start 启动多播监听 + 周期性公告
func (d *Discovery) Start() error {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", MulticastGroup, DefaultPort))
	if err != nil {
		return fmt.Errorf("resolve multicast addr: %w", err)
	}

	// 加入指定接口的多播组；iface=nil 表示系统默认
	conn, err := net.ListenMulticastUDP("udp", d.iface, addr)
	if err != nil {
		return fmt.Errorf("listen multicast on %v: %w (is another LocalSend already running?)",
			ifaceName(d.iface), err)
	}
	d.conn = conn

	d.wg.Add(3)
	go d.readLoop()
	go d.announceLoop()
	go d.cleanupLoop()
	return nil
}

func ifaceName(i *net.Interface) string {
	if i == nil {
		return "auto"
	}
	return i.Name
}

// Stop 停止发现服务
func (d *Discovery) Stop() {
	close(d.stop)
	if d.conn != nil {
		_ = d.conn.Close()
	}
	d.wg.Wait()
}

// Devices 返回当前已发现的设备列表（副本）
func (d *Discovery) Devices() []DeviceInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]DeviceInfo, 0, len(d.devices))
	for _, dev := range d.devices {
		out = append(out, *dev)
	}
	return out
}

// Find 通过 fingerprint 查找设备
func (d *Discovery) Find(fingerprint string) (*DeviceInfo, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	dev, ok := d.devices[fingerprint]
	return dev, ok
}

func (d *Discovery) readLoop() {
	defer d.wg.Done()
	buf := make([]byte, 64*1024)
	for {
		select {
		case <-d.stop:
			return
		default:
		}
		d.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, remote, err := d.conn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			select {
			case <-d.stop:
				return
			default:
			}
			continue
		}
		var ann Announce
		if err := json.Unmarshal(buf[:n], &ann); err != nil {
			continue
		}
		if ann.Fingerprint == d.localFingerprint {
			continue
		}
		d.addOrUpdate(&DeviceInfo{
			Fingerprint: ann.Fingerprint,
			IP:          remote.IP.String(),
			Port:        ann.Port,
			Name:        ann.Alias,
			DeviceModel: ann.DeviceModel,
			DeviceType:  ann.DeviceType,
			Protocol:    ann.Protocol,
			Download:    ann.Download,
			LastSeen:    time.Now(),
		})
	}
}

func (d *Discovery) announceLoop() {
	defer d.wg.Done()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	d.sendAnnounce()
	for {
		select {
		case <-d.stop:
			return
		case <-ticker.C:
			d.sendAnnounce()
		}
	}
}

// cleanupLoop 定期清理过期设备
func (d *Discovery) cleanupLoop() {
	defer d.wg.Done()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-d.stop:
			return
		case <-ticker.C:
			d.cleanupExpired()
		}
	}
}

func (d *Discovery) cleanupExpired() {
	now := time.Now()
	var toRemove []string
	d.mu.RLock()
	for fp, dev := range d.devices {
		if now.Sub(dev.LastSeen) > DeviceTtl {
			toRemove = append(toRemove, fp)
		}
	}
	d.mu.RUnlock()

	if len(toRemove) > 0 {
		d.mu.Lock()
		for _, fp := range toRemove {
			delete(d.devices, fp)
		}
		cb := d.callbacks
		d.mu.Unlock()
		for _, fp := range toRemove {
			if cb.OnDeviceLost != nil {
				cb.OnDeviceLost(fp)
			}
		}
	}
}

func (d *Discovery) sendAnnounce() {
	if d.conn == nil {
		return
	}
	d.mu.RLock()
	port := d.localPort
	d.mu.RUnlock()
	ann := Announce{
		Alias:       d.localName,
		Version:     "2.0",
		DeviceModel: d.localDevModel,
		DeviceType:  d.localDevType,
		Fingerprint: d.localFingerprint,
		Port:        port,
		Protocol:    d.localProtocol,
		Download:    true,
	}
	data, err := json.Marshal(ann)
	if err != nil {
		return
	}
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", MulticastGroup, DefaultPort))
	if err != nil {
		return
	}
	_, _ = d.conn.WriteToUDP(data, addr)
}

func (d *Discovery) addOrUpdate(dev *DeviceInfo) {
	d.mu.Lock()
	cb := d.callbacks
	existing, ok := d.devices[dev.Fingerprint]
	if ok {
		// 检查是否有实际信息变更
		changed := existing.Name != dev.Name || existing.Port != dev.Port ||
			existing.Protocol != dev.Protocol || existing.Download != dev.Download ||
			existing.IP != dev.IP || existing.DeviceModel != dev.DeviceModel ||
			existing.DeviceType != dev.DeviceType
		existing.Name = dev.Name
		existing.Port = dev.Port
		existing.Protocol = dev.Protocol
		existing.Download = dev.Download
		existing.DeviceModel = dev.DeviceModel
		existing.DeviceType = dev.DeviceType
		existing.IP = dev.IP
		existing.LastSeen = dev.LastSeen
		d.mu.Unlock()
		if changed && cb.OnDeviceUpdate != nil {
			cb.OnDeviceUpdate(*existing)
		}
	} else {
		d.devices[dev.Fingerprint] = dev
		d.mu.Unlock()
		if cb.OnDeviceFound != nil {
			cb.OnDeviceFound(*dev)
		}
	}
}
