package basispoints

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// noticeResponseID 标记本次响应由代理生成而不是上游返回。
const noticeResponseID = "resp_basispoints_notice"

//// 把无法转发的请求合成为一条助手消息的 SSE 响应，使会话继续而不是失败 [@x380kkm 2026-09-25] ////
func NoticeStream(model, text string) io.ReadCloser {
	item := object{
		"type": "message", "id": "msg_basispoints_notice", "role": "assistant", "status": "completed",
		"content": []any{object{"type": "output_text", "text": text, "annotations": []any{}}},
	}
	response := func(status string, output []any) object {
		return object{
			"id": noticeResponseID, "object": "response", "status": status, "model": model,
			"output": output, "usage": object{"input_tokens": 0, "output_tokens": 0, "total_tokens": 0},
		}
	}
	events := []struct {
		kind    string
		payload object
	}{
		{"response.created", object{"response": response("in_progress", []any{})}},
		{"response.in_progress", object{"response": response("in_progress", []any{})}},
		{"response.output_item.added", object{"output_index": 0, "item": object{
			"type": "message", "id": "msg_basispoints_notice", "role": "assistant", "status": "in_progress", "content": []any{},
		}}},
		{"response.content_part.added", object{"output_index": 0, "item_id": "msg_basispoints_notice", "content_index": 0,
			"part": object{"type": "output_text", "text": "", "annotations": []any{}}}},
		{"response.output_text.delta", object{"output_index": 0, "item_id": "msg_basispoints_notice", "content_index": 0, "delta": text}},
		{"response.output_text.done", object{"output_index": 0, "item_id": "msg_basispoints_notice", "content_index": 0, "text": text}},
		{"response.content_part.done", object{"output_index": 0, "item_id": "msg_basispoints_notice", "content_index": 0,
			"part": object{"type": "output_text", "text": text, "annotations": []any{}}}},
		{"response.output_item.done", object{"output_index": 0, "item": item}},
		{"response.completed", object{"response": response("completed", []any{item})}},
	}
	var builder strings.Builder
	for sequence, event := range events {
		event.payload["type"] = event.kind
		event.payload["sequence_number"] = sequence
		raw, err := json.Marshal(event.payload)
		if err != nil {
			continue
		}
		fmt.Fprintf(&builder, "event: %s\ndata: %s\n\n", event.kind, raw)
	}
	return io.NopCloser(strings.NewReader(builder.String()))
}

// NoticeText 说明本次请求为什么没有发往 Basispoints，供客户端直接显示。
func NoticeText(err error) string {
	return "【Basispoints】本次请求没有发送到 Excel 上游：" + UserMessage(err) +
		"\n\n该账号已开启 Excel 通道，不会改用原 Codex 上游。请按上面的说明调整后重试。"
}
