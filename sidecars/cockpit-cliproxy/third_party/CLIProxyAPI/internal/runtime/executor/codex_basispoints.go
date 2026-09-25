// audience: internal
// # codex-basispoints
//
// 账号级 Basispoints 通道。凭据元数据 `basispoints` 为真的 Codex OAuth 账号，把 /responses
// 请求改发到 ChatGPT for Excel 插件的上游 `basispoints.ResponsesURL`：请求体由
// `basispoints.Prepare` 改写，客户端工具经原生 `run_officejs` 转运，上游事件流由
// `Bridge.Stream` 翻译回 Codex SSE，之后沿用执行器原有的流处理。
// 请求声明了 Basispoints 无法承接的能力（联网搜索、生图、结构化输出、强制工具、内嵌图片）时，
// 同一账号按原 Codex 通道发送。
// 前提：凭据带有 access_token 与 account_id；API Key 凭据不走此通道。
package executor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/basispoints"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

//// 按账号与会话隔离的原生工具条目回放缓存 [@x380kkm 2026-09-25] ////
var codexBasispointsReplay basispoints.ReplayCache

//// 判断凭据是否打开了 Basispoints 账号级开关 [@x380kkm 2026-09-25] ////
func codexBasispointsEnabled(auth *cliproxyauth.Auth) bool {
	if auth == nil || auth.Metadata == nil {
		return false
	}
	if auth.Attributes != nil && strings.TrimSpace(auth.Attributes["api_key"]) != "" {
		return false
	}
	switch value := auth.Metadata["basispoints"].(type) {
	case bool:
		return value
	case string:
		enabled, _ := strconv.ParseBool(strings.TrimSpace(value))
		return enabled
	}
	return false
}

//// 决定本次请求是否走 Basispoints，回退原 Codex 时记录原因 [@x380kkm 2026-09-25] ////
func codexBasispointsRouteSelected(ctx context.Context, auth *cliproxyauth.Auth, body []byte) bool {
	if !codexBasispointsEnabled(auth) {
		return false
	}
	if reason := basispoints.NativeCodexReason(body, false); reason != "" {
		helps.LogWithRequestID(ctx).Infof("basispoints: auth=%s route=native_codex reason=%s", auth.ID, reason)
		return false
	}
	return true
}

//// 构造发往 Basispoints 的上游请求，返回改写后的请求体与流翻译器 [@x380kkm 2026-09-25] ////
func newCodexBasispointsRequest(ctx context.Context, auth *cliproxyauth.Auth, body []byte, headers http.Header) (*http.Request, []byte, *basispoints.Bridge, error) {
	token, _ := codexCreds(auth)
	accountID, _ := auth.Metadata["account_id"].(string)
	token, accountID = strings.TrimSpace(token), strings.TrimSpace(accountID)
	if token == "" || accountID == "" {
		return nil, nil, nil, statusErr{code: http.StatusBadRequest, msg: "Basispoints 需要 ChatGPT access token 和账号 ID"}
	}
	if sessionID := codexBasispointsSessionID(headers); sessionID != "" {
		body, _ = sjson.SetBytes(body, "prompt_cache_key", sessionID)
	}
	upstreamBody, bridge, err := basispoints.Prepare(body, codexBasispointsScope(auth, body), &codexBasispointsReplay)
	if err != nil {
		helps.LogWithRequestID(ctx).Warnf("basispoints: auth=%s stage=prepare category=%s", auth.ID, basispoints.Category(err))
		return nil, nil, nil, statusErr{code: http.StatusBadRequest, msg: basispoints.UserMessage(err)}
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, basispoints.ResponsesURL, bytes.NewReader(upstreamBody))
	if err != nil {
		return nil, nil, nil, err
	}
	header := httpReq.Header
	header.Set("Authorization", "Bearer "+token)
	header.Set("Chatgpt-Account-Id", accountID)
	header.Set("X-OpenAI-Account-Id", accountID)
	header.Set("X-Basispoints-Auth-Mode", "chatgpt")
	header.Set("X-OpenAI-Internal-Basispoints-Client-Product", "basispoints-excel-plugin")
	header.Set("X-OpenAI-Internal-Basispoints-Client-Agent-Profile", "excel")
	header.Set("Content-Type", "application/json")
	header.Set("Accept", "text/event-stream")
	helps.LogWithRequestID(ctx).Infof("basispoints: auth=%s route=basispoints model=%s requested_effort=%s effective_effort=%s", auth.ID, gjson.GetBytes(upstreamBody, "model").String(), bridge.RequestedEffort, bridge.Effort)
	return httpReq, upstreamBody, bridge, nil
}

//// 从客户端请求头取显式会话标识 [@x380kkm 2026-09-25] ////
func codexBasispointsSessionID(headers http.Header) string {
	if headers == nil {
		return ""
	}
	for _, key := range []string{"Session-Id", "Session_id", "Conversation-Id", "Conversation_id", "X-Session-Id", "X-Session-Affinity"} {
		if value := strings.TrimSpace(headers.Get(key)); value != "" {
			return value
		}
	}
	return ""
}

//// 生成回放缓存作用域：凭据 ID 加会话摘要 [@x380kkm 2026-09-25] ////
func codexBasispointsScope(auth *cliproxyauth.Auth, body []byte) string {
	scope := auth.ID
	if conversation := gjson.GetBytes(body, "prompt_cache_key").String(); conversation != "" {
		scope += fmt.Sprintf("|%x", sha256.Sum256([]byte(conversation)))
	}
	return scope
}
