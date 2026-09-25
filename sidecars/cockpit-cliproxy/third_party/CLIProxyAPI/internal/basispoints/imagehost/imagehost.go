// audience: internal
// # imagehost
//
// Basispoints 内嵌图片的公网托管。Excel 上游只接受由它自己抓取的 HTTPS 图片链接，
// 因此本包把请求体里的 data URL 图片按内容哈希落盘，只在回环地址上提供带 HMAC 签名的
// `GET /p/img/<id>`，再由 cloudflared 快速隧道把该端口映射成公网 HTTPS 域名。
// 签名密钥与隧道域名都保存在内存；进程重启后旧链接失效，图片文件按 TTL 清理。
// 运行前提：同目录存在 cloudflared 可执行文件，且它能直连 Cloudflare 边缘（代理软件需放行）。
package imagehost

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// linkTTL 是签名链接的有效期，取整后同一时段内链接不变。
	linkTTL = 3 * time.Hour
	// assetTTL 是图片文件的保留时长。
	assetTTL = 24 * time.Hour
	// maxImageBytes 是单张图片解码后的大小上限。
	maxImageBytes = 10 << 20
	// readyPollInterval 是等待隧道就绪时两次检查的间隔。
	readyPollInterval = 250 * time.Millisecond
)

// Host 提供图片落盘、签名链接与本地服务。零值不可用，须经 New 创建。
type Host struct {
	dir     string
	secret  []byte
	port    int
	mu      sync.RWMutex
	baseURL string
}

//// 创建图片托管：准备目录、随机签名密钥并在回环地址启动只读服务 [@x380kkm 2026-09-25] ////
func New(dir string) (*Host, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("basispoints image host directory: %w", err)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("basispoints image host listener: %w", err)
	}
	host := &Host{dir: dir, secret: secret, port: listener.Addr().(*net.TCPAddr).Port}
	mux := http.NewServeMux()
	mux.HandleFunc("/p/img/", host.serve)
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = server.Serve(listener) }()
	go host.cleanup()
	return host, nil
}

// Port 是本地服务端口，隧道把它映射到公网。
func (h *Host) Port() int { return h.port }

// SetBaseURL 记录当前可用的公网基址；空值表示隧道未就绪。
func (h *Host) SetBaseURL(baseURL string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.baseURL = strings.TrimSuffix(strings.TrimSpace(baseURL), "/")
}

// Ready 报告隧道是否已提供公网基址。
func (h *Host) Ready() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.baseURL != ""
}

//// 在给定时长内等待隧道就绪，时长耗尽或上下文结束时返回当时的就绪状态 [@x380kkm 2026-09-25] ////
func (h *Host) WaitReady(ctx context.Context, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for !h.Ready() && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return h.Ready()
		case <-time.After(readyPollInterval):
		}
	}
	return h.Ready()
}

//// 按内容哈希保存图片并返回公网签名链接 [@x380kkm 2026-09-25] ////
func (h *Host) Publish(payload []byte, mediaType string) (string, error) {
	h.mu.RLock()
	baseURL := h.baseURL
	h.mu.RUnlock()
	if baseURL == "" {
		return "", errors.New("Basispoints image host is not configured; embedded images cannot become HTTPS links")
	}
	if len(payload) == 0 || len(payload) > maxImageBytes {
		return "", errors.New("Basispoints embedded image exceeds the 10MB limit")
	}
	sum := sha256.Sum256(payload)
	id := hex.EncodeToString(sum[:16])
	path := filepath.Join(h.dir, id)
	if _, err := os.Stat(path); err != nil {
		if err := writeFileAtomic(path, payload); err != nil {
			return "", fmt.Errorf("Basispoints image host could not store an image: %w", err)
		}
	}
	if err := os.WriteFile(path+".type", []byte(mediaType), 0o600); err != nil {
		return "", fmt.Errorf("Basispoints image host could not store an image: %w", err)
	}
	expiry := time.Now().Add(linkTTL).Truncate(linkTTL).Unix()
	return fmt.Sprintf("%s/p/img/%s?exp=%d&sig=%s", baseURL, id, expiry, h.sign(id, expiry)), nil
}

//// 校验签名后返回图片，支持 GET 与 HEAD [@x380kkm 2026-09-25] ////
func (h *Host) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/p/img/")
	expiry, err := strconv.ParseInt(r.URL.Query().Get("exp"), 10, 64)
	if err != nil || id == "" || strings.ContainsAny(id, `/\.`) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if time.Now().Unix() > expiry || !hmac.Equal([]byte(r.URL.Query().Get("sig")), []byte(h.sign(id, expiry))) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	payload, err := os.ReadFile(filepath.Join(h.dir, id))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	mediaType, err := os.ReadFile(filepath.Join(h.dir, id+".type"))
	if err != nil || len(mediaType) == 0 {
		mediaType = []byte("application/octet-stream")
	}
	w.Header().Set("Content-Type", string(mediaType))
	w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
	w.Header().Set("Cache-Control", "public, max-age=3600")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(payload)
}

func (h *Host) sign(id string, expiry int64) string {
	mac := hmac.New(sha256.New, h.secret)
	fmt.Fprintf(mac, "%s\n%d", id, expiry)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

//// 定期删除超过保留时长的图片文件 [@x380kkm 2026-09-25] ////
func (h *Host) cleanup() {
	for range time.Tick(time.Hour) {
		entries, err := os.ReadDir(h.dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			info, err := entry.Info()
			if err == nil && time.Since(info.ModTime()) > assetTTL {
				_ = os.Remove(filepath.Join(h.dir, entry.Name()))
			}
		}
	}
}

func writeFileAtomic(path string, payload []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".img-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	if _, err := temp.Write(payload); err != nil {
		_ = temp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Rename(name, path)
}
