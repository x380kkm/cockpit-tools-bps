package executor

import (
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

//// 压缩触发条目补在 input 末尾且不重复 [@x380kkm 2026-09-25] ////
func TestAppendCompactionTrigger(t *testing.T) {
	body := []byte(`{"model":"gpt-6-astra","input":[{"type":"message","role":"user","content":[]}]}`)
	once := appendCompactionTrigger(body)
	items := gjson.GetBytes(once, "input").Array()
	if len(items) != 2 || items[1].Get("type").String() != "compaction_trigger" {
		t.Fatalf("trigger must be appended last: %s", once)
	}
	if twice := appendCompactionTrigger(once); len(gjson.GetBytes(twice, "input").Array()) != 2 {
		t.Fatalf("trigger must not duplicate: %s", twice)
	}
	empty := appendCompactionTrigger([]byte(`{"model":"gpt-6-astra","input":"hi"}`))
	if gjson.GetBytes(empty, "input.0.type").String() != "compaction_trigger" {
		t.Fatalf("text input must become an array carrying the trigger: %s", empty)
	}
}

//// 聚合 SSE 时取终止事件里的 response 对象，失败事件报错 [@x380kkm 2026-09-25] ////
func TestCollectBasispointsResponse(t *testing.T) {
	completed := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"x\"}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\"}}\n\n"
	payload, err := collectBasispointsResponse(strings.NewReader(completed))
	if err != nil || gjson.GetBytes(payload, "id").String() != "resp_1" {
		t.Fatalf("completed response not extracted: %s, %v", payload, err)
	}
	if _, err := collectBasispointsResponse(strings.NewReader("event: response.failed\ndata: {\"type\":\"response.failed\"}\n\n")); err == nil {
		t.Fatal("failed response must surface an error")
	}
	if _, err := collectBasispointsResponse(strings.NewReader("event: ping\ndata: {}\n\n")); err == nil {
		t.Fatal("missing terminal event must surface an error")
	}
}
