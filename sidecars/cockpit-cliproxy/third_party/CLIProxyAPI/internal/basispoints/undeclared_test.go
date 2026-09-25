package basispoints

import (
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"
)

// streamCompleted 把一组上游 SSE 事件交给桥接，返回下游原文与 response.completed 里的输出条目。
func streamCompleted(t *testing.T, bridge *Bridge, wire string) ([]byte, []any) {
	t.Helper()
	body := bridge.Stream(io.NopCloser(strings.NewReader(wire)))
	defer body.Close()
	out, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	var output []any
	_ = readEvents(bytes.NewReader(out), func(_ string, data []byte) error {
		var event object
		if decode(data, &event) == nil && text(event["type"]) == "response.completed" {
			response, _ := event["response"].(object)
			output, _ = response["output"].([]any)
		}
		return nil
	})
	return out, output
}

//// 未声明工具以原名转交客户端，下一轮回放时上游看到原生条目 [@x380kkm 2026-09-25] ////
func TestUndeclaredCallRelaysAndReplaysNative(t *testing.T) {
	cache := new(ReplayCache)
	source := testSource()
	source["tools"] = []any{object{"type": "custom", "name": "exec"}}
	_, bridge := mustPrepare(t, source, "account/key/session", cache)
	native := nativeCall(object{"name": "update_plan", "arguments": object{"plan": []any{object{"step": "count", "status": "completed"}}}})
	out, output := streamCompleted(t, bridge, sse(object{"type": "response.completed", "response": object{"output": []any{native}}}))
	if len(output) != 1 || bytes.Contains(out, []byte("response.failed")) {
		t.Fatalf("undeclared call must complete the turn: %s", out)
	}
	call := output[0].(object)
	if call["type"] != "function_call" || call["name"] != "update_plan" || call["call_id"] != "call_native" ||
		!strings.Contains(text(call["arguments"]), `"step":"count"`) {
		t.Fatalf("undeclared call relayed incorrectly: %+v", call)
	}
	source["input"] = []any{message("user", "hello"), call, object{"type": "function_call_output", "call_id": "call_native", "output": "unsupported call: update_plan"}}
	next, _ := mustPrepare(t, source, "account/key/session", cache)
	var replayed object
	for _, raw := range next["input"].([]any) {
		if item := raw.(object); isTool(item) {
			replayed = item
		}
	}
	if !reflect.DeepEqual(replayed, native) {
		t.Fatalf("replay must restore the native transport item: got=%+v want=%+v", replayed, native)
	}
}

//// 客户端没有声明计划工具时，原生 update_plan 去掉宿主前缀后转交客户端 [@x380kkm 2026-09-25] ////
func TestNativePlanWithoutClientPlanToolRelays(t *testing.T) {
	source := testSource()
	source["tools"] = []any{object{"type": "custom", "name": "exec"}}
	for _, name := range []string{"update_plan", "functions.update_plan"} {
		_, bridge := mustPrepare(t, source, "", nil)
		native := object{"type": "function_call", "id": "fc_plan", "call_id": "call_plan", "name": name, "arguments": `{"plan":[]}`, "status": "completed"}
		out, output := streamCompleted(t, bridge, sse(object{"type": "response.completed", "response": object{"output": []any{native}}}))
		if len(output) != 1 || output[0].(object)["name"] != "update_plan" || output[0].(object)["arguments"] != `{"plan":[]}` {
			t.Fatalf("native plan call %q must be relayed as update_plan: %s", name, out)
		}
	}
}

//// 协议失败时已流出的条目保留，提示消息接在最大输出序号之后，并以 completed 结束 [@x380kkm 2026-09-25] ////
func TestProtocolFailureEndsWithNoticeAfterStreamedItems(t *testing.T) {
	source := testSource()
	source["tools"] = []any{object{"type": "function", "name": "shell"}}
	_, bridge := mustPrepare(t, source, "", nil)
	reply := object{"type": "message", "id": "msg_1", "role": "assistant", "status": "completed", "content": []any{object{"type": "output_text", "text": "Checking."}}}
	broken := object{"type": "function_call", "id": "fc_bad", "call_id": "call_bad", "name": "run_officejs", "arguments": `{"code":"Excel.run(...)"}`}
	wire := sse(object{"type": "response.created", "response": object{"id": "resp_up", "model": "gpt-6-astra", "status": "in_progress"}}) +
		sse(object{"type": "response.output_item.done", "output_index": 0, "item": reply}) +
		sse(object{"type": "response.output_item.done", "output_index": 3, "item": broken}) +
		sse(object{"type": "response.completed", "response": object{"id": "resp_up", "model": "gpt-6-astra", "output": []any{reply, broken}, "usage": object{"total_tokens": 7}}})
	out, output := streamCompleted(t, bridge, wire)
	if bytes.Contains(out, []byte("response.failed")) || bytes.Contains(out, []byte("response.function_call_arguments")) {
		t.Fatalf("protocol failure must not fail the turn or dispatch a tool: %s", out)
	}
	if len(output) != 2 || output[0].(object)["id"] != "msg_1" || output[1].(object)["id"] != noticeMessageID {
		t.Fatalf("completed output must keep the streamed reply and append the notice: %+v", output)
	}
	if !bytes.Contains(out, []byte(`"output_index":4`)) || !bytes.Contains(out, []byte(`"id":"resp_up"`)) || !bytes.Contains(out, []byte(`"total_tokens":7`)) {
		t.Fatalf("notice must follow the highest output index and keep the upstream response identity: %s", out)
	}
}
