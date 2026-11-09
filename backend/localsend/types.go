// LocalSend 协议类型定义
package localsend

// 默认端口和多播地址
const (
	DefaultPort      = 53317
	MulticastGroup   = "224.0.0.167"
	ProtocolVersion  = 2
)

// Announce 是 UDP 广播的数据结构（v2）
type Announce struct {
	Alias       string `json:"alias"`
	Version     string `json:"version"` // "2.0"
	DeviceModel string `json:"deviceModel,omitempty"`
	DeviceType  string `json:"deviceType"`
	Fingerprint string `json:"fingerprint"`
	Port        int    `json:"port"`
	Protocol    string `json:"protocol"` // http 或 https
	Download    bool   `json:"download"`
}

// RegisterRequest v2 注册请求（收到 announce 后回复）
type RegisterRequest struct {
	Alias       string `json:"alias"`
	Version     string `json:"version"`
	DeviceModel string `json:"deviceModel,omitempty"`
	DeviceType  string `json:"deviceType"`
	Fingerprint string `json:"fingerprint"`
	Port        int    `json:"port"`
	Protocol    string `json:"protocol"`
	Download    bool   `json:"download"`
}

// RegisterResponse 注册响应
type RegisterResponse struct {
	Alias       string `json:"alias"`
	Version     string `json:"version"`
	DeviceModel string `json:"deviceModel,omitempty"`
	DeviceType  string `json:"deviceType"`
	Fingerprint string `json:"fingerprint"`
	Port        int    `json:"port"`
	Protocol    string `json:"protocol"`
	Download    bool   `json:"download"`
}

// FileMetadata 文件元数据，在 send-request 中携带
type FileMetadata struct {
	ID       string `json:"id"`
	FileName string `json:"fileName"`
	Size     int64  `json:"size"`
	MIME     string `json:"mimeType"`
	Hash     string `json:"hash,omitempty"`

	// LocalPath 文件在本机频道目录里的落盘路径（不对外暴露，Snapshot 会清空）
	LocalPath string `json:"localPath,omitempty"`
}

// SendRequest v2 发送请求
type SendRequest struct {
	Info    SendInfo       `json:"info"`
	Files   []FileMetadata `json:"files"`
	SkipPin bool           `json:"skipPin"`
}

// SendInfo 发送端设备信息
type SendInfo struct {
	Alias       string `json:"alias"`
	Version     string `json:"version"`
	DeviceModel string `json:"deviceModel,omitempty"`
	DeviceType  string `json:"deviceType"`
	Fingerprint string `json:"fingerprint"`
	Port        int    `json:"port"`
	Protocol    string `json:"protocol"`
	Download    bool   `json:"download"`
}

// SendResponse 接收端对 send-request 的响应
type SendResponse struct {
	SessionID string `json:"sessionId"`
	Status    string `json:"status"` // "accept", "decline", "save"
}

// CancelRequest 取消传输
type CancelRequest struct {
	SessionID string `json:"sessionId"`
}
