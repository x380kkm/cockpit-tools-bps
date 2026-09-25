package basispoints

import (
	"errors"
	"io"
	"strings"
	"testing"
)

//// 模型不可用的 403 与请求被拒的 400 转成提示，账号级错误留给号池 [@x380kkm 2026-09-25] ////
func TestRejectionNoticeTextSeparatesRequestAndAccountErrors(t *testing.T) {
	modelAccess := []byte(`{"error":{"type":"invalid_request_error","code":"basispoints_model_access_changed","message":"Model access has changed."}}`)
	if text, ok := RejectionNoticeText("gpt-6-sol", 403, modelAccess); !ok || !strings.Contains(text, "gpt-6-sol") || !strings.Contains(text, "Model access has changed.") {
		t.Fatalf("model access rejection must name the model: %q, %v", text, ok)
	}
	invalid := []byte(`{"error":{"message":"Invalid 'input[9].id'."}}`)
	if text, ok := RejectionNoticeText("gpt-6-astra", 400, invalid); !ok || !strings.Contains(text, "Invalid 'input[9].id'.") {
		t.Fatalf("invalid request must quote the upstream message: %q, %v", text, ok)
	}
	if text, ok := RejectionNoticeText("gpt-6-astra", 400, []byte("plain failure")); !ok || !strings.Contains(text, "plain failure") {
		t.Fatalf("non-JSON rejection must quote the raw body: %q, %v", text, ok)
	}
	for _, status := range []int{401, 403, 429, 500, 503} {
		if _, ok := RejectionNoticeText("gpt-6-astra", status, []byte(`{"error":{"code":"other"}}`)); ok {
			t.Fatalf("status %d must stay an error for the account pool", status)
		}
	}
}

//// 提示流是一段完整的 SSE 助手消息，含文本增量与终止事件 [@x380kkm 2026-09-25] ////
func TestNoticeStreamIsACompleteAssistantTurn(t *testing.T) {
	text := NoticeText(errors.New("Basispoints cannot force the hosted tool_choice \"image_generation\""))
	if !strings.Contains(text, "没有发送到 Excel 上游") || !strings.Contains(text, "不会改用原 Codex 上游") {
		t.Fatalf("notice text must explain the channel policy: %s", text)
	}
	stream := NoticeStream("gpt-6-astra", text)
	defer stream.Close()
	raw, err := io.ReadAll(stream)
	if err != nil {
		t.Fatal(err)
	}
	output := string(raw)
	for _, want := range []string{
		"event: response.created", "event: response.output_item.added",
		"event: response.output_text.delta", "event: response.output_item.done",
		"event: response.completed", `"role":"assistant"`, `"model":"gpt-6-astra"`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("notice stream lacks %q: %s", want, output)
		}
	}
	var terminal object
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "data:") && strings.Contains(line, "response.completed") {
			if err := decode([]byte(strings.TrimPrefix(line, "data: ")), &terminal); err != nil {
				t.Fatal(err)
			}
		}
	}
	response, _ := terminal["response"].(object)
	items, _ := response["output"].([]any)
	if response["status"] != "completed" || len(items) != 1 {
		t.Fatalf("terminal event must carry one completed message: %+v", terminal)
	}
}
