package basispoints

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func preparedText(t *testing.T, source object) (string, *Bridge) {
	t.Helper()
	body, bridge := mustPrepare(t, source, "scope", nil)
	encoded, _ := json.Marshal(body)
	return string(encoded), bridge
}

//// 推理模式、强制工具与结构化输出改写为提示词指令而不是拒绝 [@x380kkm 2026-09-25] ////
func TestEmulatedCapabilitiesBecomeDirectives(t *testing.T) {
	shell := object{"type": "function", "name": "shell", "parameters": object{"type": "object"}}
	cases := []struct {
		name  string
		patch object
		want  string
	}{
		{"required tool choice", object{"tools": []any{shell}, "tool_choice": "required"}, "You must call at least one client tool"},
		{"forced function", object{"tools": []any{shell}, "tool_choice": object{"type": "function", "name": "shell"}}, "You must call the client tool shell"},
		{"forced web search", object{"tool_choice": object{"type": "web_search"}}, "You must call native web_search"},
		{"json schema", object{"text": object{"format": object{"type": "json_schema", "name": "title", "schema": object{"type": "object", "properties": object{"title": object{"type": "string"}}}}}}, `\"title\":{\"type\":\"string\"}`},
		{"json object", object{"response_format": object{"type": "json_object"}}, "exactly one valid JSON object"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := testSource()
			for key, value := range tc.patch {
				source[key] = value
			}
			encoded, _ := preparedText(t, source)
			if !strings.Contains(encoded, tc.want) {
				t.Fatalf("directive %q missing from %s", tc.want, encoded)
			}
			if strings.Contains(encoded, `"tool_choice"`) || strings.Contains(encoded, `"response_format"`) {
				t.Fatalf("emulated fields must not reach the wire body: %s", encoded)
			}
		})
	}
	source := testSource()
	source["reasoning"] = object{"effort": "high", "mode": "pro"}
	_, bridge := preparedText(t, source)
	if len(bridge.Warnings) != 1 || !strings.Contains(bridge.Warnings[0], "reasoning mode pro ignored") {
		t.Fatalf("ignored reasoning mode must be reported: %v", bridge.Warnings)
	}
}

//// agent_message 改写为带作者与接收者说明的用户消息 [@x380kkm 2026-09-25] ////
func TestAgentMessageBecomesUserMessage(t *testing.T) {
	source := testSource()
	source["input"] = []any{
		message("user", "start"),
		object{"type": "agent_message", "id": "amsg_1", "author": "/root", "recipient": "/root/worker", "content": []any{
			object{"type": "input_text", "text": "NEW_TASK"},
			object{"type": "encrypted_content", "encrypted_content": "只读核对资源"},
		}},
	}
	body, _ := mustPrepare(t, source, "scope", nil)
	items := body["input"].([]any)
	converted := items[len(items)-1].(object)
	content := converted["content"].([]any)[0].(object)
	if converted["type"] != "message" || converted["role"] != "user" || converted["id"] != nil {
		t.Fatalf("agent message not converted: %+v", converted)
	}
	for _, want := range []string{"[agent message from /root to /root/worker]", "NEW_TASK", "只读核对资源"} {
		if !strings.Contains(text(content["text"]), want) {
			t.Fatalf("converted text lacks %q: %q", want, content["text"])
		}
	}
}

//// 扣留工具参数事件时写出 response.in_progress 保活事件 [@x380kkm 2026-09-25] ////
func TestKeepaliveWhileWithholdingToolEvents(t *testing.T) {
	previous := keepaliveInterval
	keepaliveInterval = 0
	defer func() { keepaliveInterval = previous }()
	_, bridge := mustPrepare(t, testSource(), "scope", nil)
	upstream := "event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"{\"}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n"
	body := bridge.Stream(io.NopCloser(strings.NewReader(upstream)))
	defer body.Close()
	output, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), "event: response.in_progress\n") || !strings.Contains(string(output), "response.completed") {
		t.Fatalf("withheld tool events must produce in_progress keepalive events: %s", output)
	}
}
