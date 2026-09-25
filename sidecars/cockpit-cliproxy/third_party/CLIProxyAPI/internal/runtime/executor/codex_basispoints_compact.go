package executor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

//// 会话压缩改由 /responses 携带 compaction_trigger 完成，聚合流式结果回一次性 JSON [@x380kkm 2026-09-25] ////
func (e *CodexExecutor) executeBasispointsCompact(ctx context.Context, auth *cliproxyauth.Auth, model string, body []byte, headers http.Header) ([]byte, error) {
	plan, err := newCodexBasispointsPlan(ctx, auth, model, appendCompactionTrigger(body), headers)
	if err != nil {
		return nil, err
	}
	if plan.notice != nil {
		defer func() { _ = plan.notice.Close() }()
		return collectBasispointsResponse(plan.notice)
	}
	httpClient := helps.NewUtlsHTTPClient(ctx, e.cfg, auth, 0)
	httpResp, err := httpClient.Do(plan.request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = httpResp.Body.Close() }()
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		payload, _ := io.ReadAll(httpResp.Body)
		if rejection := codexBasispointsRejectionNotice(ctx, model, httpResp.StatusCode, payload); rejection != nil {
			defer func() { _ = rejection.Close() }()
			return collectBasispointsResponse(rejection)
		}
		return nil, newCodexStatusErrWithCooling(httpResp.StatusCode, payload, e.modelLevelCooling())
	}
	return collectBasispointsResponse(plan.bridge.Stream(httpResp.Body))
}

//// 在 input 末尾补一个压缩触发条目 [@x380kkm 2026-09-25] ////
func appendCompactionTrigger(body []byte) []byte {
	trigger := json.RawMessage(`{"type":"compaction_trigger"}`)
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		out, _ := sjson.SetBytes(body, "input", []any{})
		body = out
	}
	items := gjson.GetBytes(body, "input").Array()
	if len(items) > 0 && items[len(items)-1].Get("type").String() == "compaction_trigger" {
		return body
	}
	out, _ := sjson.SetRawBytes(body, fmt.Sprintf("input.%d", len(items)), trigger)
	return out
}

//// 读取 SSE 直到终止事件，返回其中的 response 对象 [@x380kkm 2026-09-25] ////
func collectBasispointsResponse(stream io.Reader) ([]byte, error) {
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 64*1024), 16<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		switch gjson.GetBytes(data, "type").String() {
		case "response.completed", "response.incomplete":
			response := gjson.GetBytes(data, "response")
			if !response.Exists() {
				return nil, fmt.Errorf("Basispoints compaction response is missing its response object")
			}
			return []byte(response.Raw), nil
		case "response.failed", "error":
			return nil, fmt.Errorf("Basispoints compaction failed: %s", bytes.TrimSpace(data))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("Basispoints compaction stream ended before a terminal event")
}
