package basispoints

import (
	"strings"
	"testing"
)

//// 工具结果条目的 ID 在回放时统一为 fc 前缀，已有 fc 前缀的 ID 原样保留 [@x380kkm 2026-09-25] ////
func TestToolOutputIDsUseFunctionPrefix(t *testing.T) {
	cases := []struct {
		name, toolType, callType, outputType, outputID, wantID string
	}{
		{"custom output with ctco id", "custom", "custom_tool_call", "custom_tool_call_output", "ctco_01a0d7a0-9d42-72d0-afba-7ef12b87418d", "fc_call_tool"},
		{"custom output with fco id", "custom", "custom_tool_call", "custom_tool_call_output", "fco_kept", "fco_kept"},
		{"function output with fc id", "function", "function_call", "function_call_output", "fc_kept", "fc_kept"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := testSource()
			source["tools"] = []any{object{"type": tc.toolType, "name": "tool"}}
			call := object{"type": tc.callType, "id": "ctc_client", "call_id": "call_tool", "name": "tool", "status": "completed"}
			if tc.toolType == "custom" {
				call["input"] = "raw input"
			} else {
				call["arguments"] = `{"value":1}`
			}
			output := object{"type": tc.outputType, "id": tc.outputID, "call_id": "call_tool", "output": "done"}
			source["input"] = []any{message("user", "run the tool"), call, output}
			prepared, _ := mustPrepare(t, source, "account/key/session", new(ReplayCache))
			items := prepared["input"].([]any)
			replayedCall := items[len(items)-2].(object)
			replayedOutput := items[len(items)-1].(object)
			if !strings.HasPrefix(text(replayedCall["id"]), "fc_") {
				t.Fatalf("replayed call id = %q, want fc_ prefix", replayedCall["id"])
			}
			if replayedOutput["type"] != "function_call_output" || replayedOutput["id"] != tc.wantID {
				t.Fatalf("replayed output = %+v, want function_call_output with id %q", replayedOutput, tc.wantID)
			}
		})
	}
}
