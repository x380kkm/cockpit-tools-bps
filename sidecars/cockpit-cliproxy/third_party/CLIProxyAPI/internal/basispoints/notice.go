package basispoints

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// noticeResponseID 标记本次响应由代理生成而不是上游返回。
const noticeResponseID = "resp_basispoints_notice"

// noticeMessageID 是代理合成的提示消息条目 ID。
const noticeMessageID = "msg_basispoints_notice"

// noticeEvent 是提示消息的一个 SSE 事件，序号由写出方统一分配。
type noticeEvent struct {
	kind    string
	payload object
}

// noticeMessage 构造一条已完成的助手提示消息条目。
func noticeMessage(text string) object {
	return object{
		"type": "message", "id": noticeMessageID, "role": "assistant", "status": "completed",
		"content": []any{object{"type": "output_text", "text": text, "annotations": []any{}}},
	}
}

//// 生成一条助手提示消息从新增到完成的条目事件 [@x380kkm 2026-09-25] ////
func noticeItemEvents(index int, text string) []noticeEvent {
	part := func(text string) object { return object{"type": "output_text", "text": text, "annotations": []any{}} }
	return []noticeEvent{
		{"response.output_item.added", object{"output_index": index, "item": object{
			"type": "message", "id": noticeMessageID, "role": "assistant", "status": "in_progress", "content": []any{},
		}}},
		{"response.content_part.added", object{"output_index": index, "item_id": noticeMessageID, "content_index": 0, "part": part("")}},
		{"response.output_text.delta", object{"output_index": index, "item_id": noticeMessageID, "content_index": 0, "delta": text}},
		{"response.output_text.done", object{"output_index": index, "item_id": noticeMessageID, "content_index": 0, "text": text}},
		{"response.content_part.done", object{"output_index": index, "item_id": noticeMessageID, "content_index": 0, "part": part(text)}},
		{"response.output_item.done", object{"output_index": index, "item": noticeMessage(text)}},
	}
}

//// 把无法转发的请求合成为一条助手消息的 SSE 响应，使会话继续而不是失败 [@x380kkm 2026-09-25] ////
func NoticeStream(model, text string) io.ReadCloser {
	response := func(status string, output []any) object {
		return object{
			"id": noticeResponseID, "object": "response", "status": status, "model": model,
			"output": output, "usage": object{"input_tokens": 0, "output_tokens": 0, "total_tokens": 0},
		}
	}
	events := []noticeEvent{
		{"response.created", object{"response": response("in_progress", []any{})}},
		{"response.in_progress", object{"response": response("in_progress", []any{})}},
	}
	events = append(events, noticeItemEvents(0, text)...)
	events = append(events, noticeEvent{"response.completed", object{"response": response("completed", []any{noticeMessage(text)})}})
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

//// 把本轮协议失败写成一条助手提示消息并以 completed 结束，账号不会因此被冷却 [@x380kkm 2026-09-25] ////
func emitProtocolNotice(emit func(string, object) error, snapshot object, finished []any, index int, failure error) error {
	text := ProtocolNoticeText(failure)
	for _, event := range noticeItemEvents(index, text) {
		if err := emit(event.kind, event.payload); err != nil {
			return err
		}
	}
	response := object{"id": noticeResponseID, "object": "response"}
	for key, value := range snapshot {
		response[key] = value
	}
	response["status"], response["error"], response["incomplete_details"] = "completed", nil, nil
	response["output"] = append(append([]any{}, finished...), noticeMessage(text))
	return emit("response.completed", object{"response": response})
}

// NoticeText 说明本次请求为什么没有发往 Basispoints，供客户端直接显示。
func NoticeText(err error) string {
	return "【Basispoints】本次请求没有发送到 Excel 上游：" + UserMessage(err) +
		"\n\n该账号已开启 Excel 通道，不会改用原 Codex 上游。请按上面的说明调整后重试。"
}

// ProtocolNoticeText 说明 Excel 上游本轮的回复为什么无法转交客户端，供客户端直接显示。
func ProtocolNoticeText(err error) string {
	return "【Basispoints】Excel 上游本轮的回复无法转交给客户端：" + ProtocolFailureMessage(err) +
		"\n\n会话可以继续：回复“继续”即可让模型重做这一步。"
}

// modelAccessCode 是 Excel 上游拒绝当前账号使用所请求模型时返回的错误码。
const modelAccessCode = "basispoints_model_access_changed"

// maxRejectionDetail 是提示文字里引用上游原始错误的最大字节数。
const maxRejectionDetail = 600

//// 把 Excel 上游对请求本身的拒绝写成提示文字；账号级错误返回 false，交给号池处理 [@x380kkm 2026-09-25] ////
func RejectionNoticeText(model string, status int, body []byte) (string, bool) {
	var payload object
	_ = decode(body, &payload)
	detail, _ := payload["error"].(object)
	message := strings.TrimSpace(text(detail["message"]))
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	if len(message) > maxRejectionDetail {
		message = message[:maxRejectionDetail] + "…"
	}
	switch {
	case status == http.StatusForbidden && text(detail["code"]) == modelAccessCode:
		return "【Basispoints】该账号的 Excel 通道不提供模型 " + model + "，本次请求没有执行。请在客户端切换到其他模型后重试。" +
			"\n\n该账号已开启 Excel 通道，不会改用原 Codex 上游。（" + message + "）", true
	case status == http.StatusBadRequest:
		return "【Basispoints】Excel 上游拒绝了本次请求，没有执行任何操作：" + message +
			"\n\n会话可以继续；如果每次都出现同一条说明，请新建会话。", true
	}
	return "", false
}
