// LocalSend HTTP 客户端：向对端发送请求、上传文件
package localsend

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// Client 是 LocalSend HTTP 客户端
type Client struct {
	http     *http.Client
	scheme   string // "http" 或 "https"
	sourceIP string // 空串表示系统默认
}

// NewClient 创建一个客户端；sourceIP 为空或 "0.0.0.0" 则用系统默认源地址
func NewClient(scheme, sourceIP string) *Client {
	transport := &http.Transport{
		MaxIdleConns:          64,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	if sourceIP != "" && sourceIP != "0.0.0.0" {
		// 绑定源地址，让出站请求走指定网卡
		localAddr, err := net.ResolveTCPAddr("tcp", fmt.Sprintf("%s:0", sourceIP))
		if err == nil {
			dialer := &net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
				LocalAddr: localAddr,
			}
			transport.DialContext = dialer.DialContext
		}
	}
	return &Client{
		http:     &http.Client{Timeout: 30 * time.Second, Transport: transport},
		scheme:   scheme,
		sourceIP: sourceIP,
	}
}

func (c *Client) URL(ip string, port int, path string) string {
	return fmt.Sprintf("%s://%s:%d/api/localsend/v%d%s", c.scheme, ip, port, ProtocolVersion, path)
}

// Register 向目标设备发送注册请求（双向确认）
func (c *Client) Register(ip string, port int, req *RegisterRequest) (*RegisterResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequest("POST", c.URL(ip, port, "/register"), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("register: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("register %d: %s", resp.StatusCode, string(b))
	}
	var out RegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SendRequest 向目标设备发送文件请求
func (c *Client) SendRequest(ip string, port int, req *SendRequest) (*SendResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequest("POST", c.URL(ip, port, "/send-request"), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send-request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("send-request %d: %s", resp.StatusCode, string(b))
	}
	var out SendResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Upload 将文件流上传到接收端
func (c *Client) Upload(ip string, port int, sessionID, fileID string, reader io.Reader, size int64) error {
	url := fmt.Sprintf("%s?sessionId=%s&fileId=%s", c.URL(ip, port, "/upload"), sessionID, fileID)
	httpReq, err := http.NewRequest("POST", url, reader)
	if err != nil {
		return err
	}
	httpReq.ContentLength = size
	httpReq.Header.Set("Content-Type", "application/octet-stream")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("upload: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

// Cancel 取消一个进行中的传输
func (c *Client) Cancel(ip string, portStr, sessionID string) error {
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	if port == 0 {
		port = DefaultPort
	}
	body, _ := json.Marshal(CancelRequest{SessionID: sessionID})
	httpReq, err := http.NewRequest("POST", c.URL(ip, port, "/cancel"), bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("cancel: %w", err)
	}
	defer resp.Body.Close()
	// cancel 往往返回 204
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("cancel %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

// Ping 探测端口可达性
func (c *Client) Ping(ip string, port int) error {
	httpReq, err := http.NewRequest("GET", c.URL(ip, port, "/info"), nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("ping: %w", err)
	}
	resp.Body.Close()
	return nil
}
