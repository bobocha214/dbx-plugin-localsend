// 网络接口枚举与选择
package localsend

import (
	"fmt"
	"net"
	"sort"
	"strings"
)

// InterfaceInfo 给 DBX 前端展示的网卡信息
type InterfaceInfo struct {
	Name    string   `json:"name"`
	Index   int      `json:"index"`
	MTU     int      `json:"mtu"`
	Flags   string   `json:"flags"`
	MAC     string   `json:"mac"`     // 硬件地址，用作本机唯一 ID
	IPv4    []string `json:"ipv4"`
	IPv6    []string `json:"ipv6"`
	IsLoop  bool     `json:"isLoopback"`
	IsUp    bool     `json:"isUp"`
}

// ListInterfaces 返回本机所有网卡（按名字排序）
func ListInterfaces() []InterfaceInfo {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	sort.Slice(ifaces, func(i, j int) bool { return ifaces[i].Name < ifaces[j].Name })

	out := make([]InterfaceInfo, 0, len(ifaces))
	for _, iface := range ifaces {
		addrs, _ := iface.Addrs()
		info := InterfaceInfo{
			Name:   iface.Name,
			Index:  iface.Index,
			MTU:    iface.MTU,
			Flags:  iface.Flags.String(),
			MAC:    iface.HardwareAddr.String(),
			IsLoop: iface.Flags&net.FlagLoopback != 0,
			IsUp:   iface.Flags&net.FlagUp != 0,
		}
		for _, a := range addrs {
			switch v := a.(type) {
			case *net.IPNet:
				if v.IP.To4() != nil {
					info.IPv4 = append(info.IPv4, v.IP.String())
				} else {
					info.IPv6 = append(info.IPv6, v.IP.String())
				}
			case *net.IPAddr:
				if v.IP.To4() != nil {
					info.IPv4 = append(info.IPv4, v.IP.String())
				} else {
					info.IPv6 = append(info.IPv6, v.IP.String())
				}
			}
		}
		out = append(out, info)
	}
	return out
}

// PickInterface 按用户输入选网卡。
func PickInterface(spec string) (*net.Interface, error) {
	if spec == "" || spec == "auto" || spec == "default" {
		return SelectBestInterface(), nil
	}
	if _, err := fmt.Sscanf(spec, "%d", new(int)); err == nil {
		if iface, err := net.InterfaceByName(spec); err == nil {
			return iface, nil
		}
	}
	if iface, err := net.InterfaceByName(spec); err == nil {
		return iface, nil
	}
	ip := net.ParseIP(spec)
	if ip != nil {
		ifaces, err := net.Interfaces()
		if err != nil {
			return nil, err
		}
		for _, iface := range ifaces {
			addrs, _ := iface.Addrs()
			for _, a := range addrs {
				var candidate net.IP
				switch v := a.(type) {
				case *net.IPNet:
					candidate = v.IP
				case *net.IPAddr:
					candidate = v.IP
				}
				if candidate != nil && candidate.Equal(ip) {
					return &iface, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("找不到网卡 %q", spec)
}

// SelectBestInterface 挑一个最可能出网的接口
func SelectBestInterface() *net.Interface {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	sort.Slice(ifaces, func(i, j int) bool { return ifaces[i].Index < ifaces[j].Index })
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			switch v := a.(type) {
			case *net.IPNet:
				if v.IP.To4() != nil {
					return &iface
				}
			case *net.IPAddr:
				if v.IP.To4() != nil {
					return &iface
				}
			}
		}
	}
	return nil
}

// FirstIPv4 返回指定接口上的第一个 IPv4
func FirstIPv4(iface *net.Interface) string {
	if iface == nil {
		return ""
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		var ip net.IP
		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if ip != nil && ip.To4() != nil {
			return ip.String()
		}
	}
	return ""
}

// MACAddress 返回指定接口的 MAC；若 nil 则选 BestInterface 的
func MACAddress(iface *net.Interface) string {
	if iface == nil {
		iface = SelectBestInterface()
	}
	if iface == nil {
		return ""
	}
	return iface.HardwareAddr.String()
}

// SanitizeMAC 把 AA:BB:CC:DD:EE:FF 转成 aabbccddeeff（去掉冒号小写），方便当唯一 ID 用
func SanitizeMAC(mac string) string {
	return strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(mac, ":", ""), "-", ""))
}

// DeviceID 机器唯一 ID：sanitize 后的 MAC + IP，这样换网卡时 IP 不同也能区分
func DeviceID(mac, ip string) string {
	return SanitizeMAC(mac) + "@" + ip
}
