package executor

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/basispoints"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/basispoints/imagehost"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	log "github.com/sirupsen/logrus"
)

// basispointsTunnelStartup 是隧道拉起后图片请求仍会等待其就绪的时段。
const basispointsTunnelStartup = 30 * time.Second

var (
	basispointsImageHostOnce    sync.Once
	basispointsImageHost        *imagehost.Host
	basispointsImageHostStarted time.Time
)

//// 首次使用时准备图片托管并拉起 cloudflared 隧道 [@x380kkm 2026-09-25] ////
func codexBasispointsImageHost() *imagehost.Host {
	basispointsImageHostOnce.Do(func() {
		basispointsImageHostStarted = time.Now()
		cloudflared := imagehost.CloudflaredPath()
		if cloudflared == "" {
			log.Warn("basispoints image host disabled: cockpit-bps-cloudflared not found next to the sidecar")
			return
		}
		dir := filepath.Join(os.TempDir(), "cockpit-basispoints-images")
		host, err := imagehost.New(dir)
		if err != nil {
			log.Warnf("basispoints image host disabled: %v", err)
			return
		}
		basispointsImageHost = host
		go imagehost.RunTunnel(context.Background(), host, cloudflared, func(format string, args ...any) {
			log.Infof(format, args...)
		})
	})
	return basispointsImageHost
}

//// 确保图片隧道已拉起，把请求体里的内嵌图片换成公网签名链接，并归一 detail 取值 [@x380kkm 2026-09-25] ////
func codexBasispointsHostImages(ctx context.Context, body []byte) []byte {
	host := codexBasispointsImageHost()
	converted := 0
	for _, field := range []string{"content", "output"} {
		for index := range gjson.GetBytes(body, "input").Array() {
			path := fmt.Sprintf("input.%d.%s", index, field)
			parts := gjson.GetBytes(body, path)
			if !parts.IsArray() {
				continue
			}
			for part := range parts.Array() {
				partPath := fmt.Sprintf("%s.%d", path, part)
				if gjson.GetBytes(body, partPath+".type").String() != "input_image" {
					continue
				}
				if gjson.GetBytes(body, partPath+".detail").String() == "original" {
					body, _ = sjson.SetBytes(body, partPath+".detail", "high")
				}
				raw := gjson.GetBytes(body, partPath+".image_url").String()
				if !basispoints.IsDataURL(raw) {
					continue
				}
				link, err := publishInlineImage(ctx, host, raw)
				if err != nil {
					helps.LogWithRequestID(ctx).Warnf("basispoints: image hosting failed: %v", err)
					continue
				}
				body, _ = sjson.SetBytes(body, partPath+".image_url", link)
				body, _ = sjson.DeleteBytes(body, partPath+".file_id")
				converted++
			}
		}
	}
	if converted > 0 {
		helps.LogWithRequestID(ctx).Infof("basispoints: hosted %d embedded images", converted)
	}
	return body
}

//// 解码 data URL 并交给图片托管发布，隧道启动期内先等待其就绪 [@x380kkm 2026-09-25] ////
func publishInlineImage(ctx context.Context, host *imagehost.Host, raw string) (string, error) {
	if host == nil || !host.WaitReady(ctx, time.Until(basispointsImageHostStarted.Add(basispointsTunnelStartup))) {
		return "", fmt.Errorf("Basispoints image host is not configured; embedded images cannot become HTTPS links")
	}
	comma := strings.IndexByte(raw, ',')
	if comma < 0 || !strings.Contains(raw[:comma], ";base64") {
		return "", fmt.Errorf("Basispoints embedded image payload is not a recognized image format")
	}
	payload, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw[comma+1:]))
	if err != nil {
		return "", fmt.Errorf("Basispoints embedded image payload is not a recognized image format")
	}
	mediaType := http.DetectContentType(payload)
	switch mediaType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
	default:
		return "", fmt.Errorf("Basispoints embedded image payload is not a recognized image format")
	}
	return host.Publish(payload, mediaType)
}
