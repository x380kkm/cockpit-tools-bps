package imagehost

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// quickTunnelHostPattern 匹配 cloudflared 在日志中公布的临时域名。
var quickTunnelHostPattern = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

// tunnelRestartDelay 是隧道退出后的重启间隔。
const tunnelRestartDelay = 5 * time.Second

// Logf 接收隧道运行状态，由调用方接到自己的日志。
type Logf func(format string, args ...any)

//// 查找与 sidecar 同目录的 cloudflared 可执行文件 [@x380kkm 2026-09-25] ////
func CloudflaredPath() string {
	name := "cockpit-bps-cloudflared"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	path := filepath.Join(filepath.Dir(executable), name)
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

//// 持续运行 cloudflared 快速隧道，把公网域名交给图片托管 [@x380kkm 2026-09-25] ////
func RunTunnel(ctx context.Context, host *Host, cloudflared string, logf Logf) {
	for ctx.Err() == nil {
		if err := runOnce(ctx, host, cloudflared, logf); err != nil && ctx.Err() == nil {
			logf("basispoints image tunnel stopped: %v", err)
		}
		host.SetBaseURL("")
		select {
		case <-ctx.Done():
		case <-time.After(tunnelRestartDelay):
		}
	}
}

func runOnce(ctx context.Context, host *Host, cloudflared string, logf Logf) error {
	// QUIC 在被代理接管的网络里握手失败，固定使用 http2 传输。
	command := exec.CommandContext(ctx, cloudflared,
		"tunnel", "--no-autoupdate", "--protocol", "http2",
		"--url", "http://127.0.0.1:"+strconv.Itoa(host.Port()))
	output, err := command.StderrPipe()
	if err != nil {
		return err
	}
	command.Stdout = io.Discard
	if err := command.Start(); err != nil {
		return err
	}
	go func() {
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 64*1024), 1<<20)
		hostname, ready, warned := "", false, false
		for scanner.Scan() {
			line := scanner.Text()
			if match := quickTunnelHostPattern.FindString(line); match != "" && hostname == "" {
				hostname = match
			}
			// 仅公布域名不代表隧道可用：边缘连接注册成功后才交给图片托管。
			if !ready && hostname != "" && strings.Contains(line, "Registered tunnel connection") {
				host.SetBaseURL(hostname)
				ready = true
				logf("basispoints image tunnel ready: %s", hostname)
			}
			// cloudflared 每秒重试，同一次隧道尝试只提示一次。
			if !warned && strings.Contains(line, "Unable to establish connection with Cloudflare edge") {
				warned = true
				logf("basispoints image tunnel cannot reach Cloudflare: let %s connect directly in your proxy software", filepath.Base(cloudflared))
			}
		}
	}()
	return command.Wait()
}
