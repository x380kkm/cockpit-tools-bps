package executor

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// scriptedDoer 依次返回预设的状态码与响应体，并记录每次收到的请求体。
type scriptedDoer struct {
	replies []struct {
		status int
		body   string
	}
	bodies []string
}

func (d *scriptedDoer) Do(request *http.Request) (*http.Response, error) {
	sent, _ := io.ReadAll(request.Body)
	d.bodies = append(d.bodies, string(sent))
	reply := d.replies[len(d.bodies)-1]
	return &http.Response{StatusCode: reply.status, Body: io.NopCloser(strings.NewReader(reply.body))}, nil
}

func script(replies ...any) *scriptedDoer {
	doer := &scriptedDoer{}
	for i := 0; i < len(replies); i += 2 {
		doer.replies = append(doer.replies, struct {
			status int
			body   string
		}{replies[i].(int), replies[i+1].(string)})
	}
	return doer
}

//// 下载图片超时的 400 用同一请求体重发，其他 400 不重发，重发次数有上限 [@x380kkm 2026-09-26] ////
func TestCodexBasispointsSendRetriesImageDownloadTimeouts(t *testing.T) {
	previous := basispointsDownloadRetryDelay
	basispointsDownloadRetryDelay = 0
	defer func() { basispointsDownloadRetryDelay = previous }()
	timeout := `{"error":{"message":"Unable to download content from the provided URL before the timeout."}}`
	body := []byte(`{"input":"x"}`)
	send := func(doer *scriptedDoer) (*http.Response, string) {
		request, _ := http.NewRequest(http.MethodPost, "https://bps.example/responses", bytes.NewReader(body))
		response, err := codexBasispointsSend(context.Background(), doer, request, body)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		return response, string(data)
	}

	recovered := script(400, timeout, 200, "ok")
	if response, data := send(recovered); response.StatusCode != 200 || data != "ok" || len(recovered.bodies) != 2 || recovered.bodies[1] != string(body) {
		t.Fatalf("timeout must be retried with the same body: %d %q %q", response.StatusCode, data, recovered.bodies)
	}
	other := script(400, `{"error":{"message":"Invalid input"}}`)
	if response, data := send(other); response.StatusCode != 400 || !strings.Contains(data, "Invalid input") || len(other.bodies) != 1 {
		t.Fatalf("other rejections must not be retried and must keep their body: %d %q", response.StatusCode, data)
	}
	exhausted := script(400, timeout, 400, timeout, 400, timeout, 200, "late")
	if response, data := send(exhausted); response.StatusCode != 400 || !strings.Contains(data, "Unable to download") || len(exhausted.bodies) != 1+basispointsDownloadRetries {
		t.Fatalf("retries must stop after %d attempts: %d %q, sent %d", basispointsDownloadRetries, response.StatusCode, data, len(exhausted.bodies))
	}
}
