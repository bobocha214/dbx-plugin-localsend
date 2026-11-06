// UDP 多播发现：发送 announce + 监听 announce
package localsend

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"
)

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

	conn *net.UDPConn
	stop chan struct{}
	wg   sync.WaitGroup
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

	d.wg.Add(2)
	go d.readLoop()
	go d.announceLoop()
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

func (d *Discovery) sendAnnounce() {
	if d.conn == nil {
		return
	}
	ann := Announce{
		Alias:       d.localName,
		Version:     "2.0",
		DeviceModel: d.localDevModel,
		DeviceType:  d.localDevType,
		Fingerprint: d.localFingerprint,
		Port:        d.localPort,
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
	defer d.mu.Unlock()
	if existing, ok := d.devices[dev.Fingerprint]; ok {
		existing.Name = dev.Name
		existing.Port = dev.Port
		existing.Protocol = dev.Protocol
		existing.Download = dev.Download
		existing.DeviceModel = dev.DeviceModel
		existing.DeviceType = dev.DeviceType
	} else {
		d.devices[dev.Fingerprint] = dev
	}
}
