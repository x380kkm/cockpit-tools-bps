package basispoints

import (
	"errors"
	"io"
	"strings"
	"testing"
)

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
