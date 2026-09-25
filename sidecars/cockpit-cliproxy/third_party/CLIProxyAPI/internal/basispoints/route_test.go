package basispoints

import (
	"strings"
	"testing"
)

func TestIsDataURL(t *testing.T) {
	if IsDataURL(" DATA:image/png;base64,AAAA") != true || IsDataURL("https://example.com/a.png") {
		t.Fatal("data URL detection must be case-insensitive and reject links")
	}
}

//// 声明 web_search 时提示模型可直接使用原生搜索，不再作为省略的托管工具 [@x380kkm 2026-09-25] ////
func TestBridgeAllowsNativeWebSearch(t *testing.T) {
	for _, declaration := range []string{`{"type":"web_search","external_web_access":false}`, `{"type":"web_search","external_web_access":true}`} {
		body, bridge, err := Prepare([]byte(`{"model":"gpt-6-astra","input":"hi","tools":[{"type":"function","name":"shell","parameters":{"type":"object"}},`+declaration+`]}`), "scope", nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(bridge.Warnings) != 0 || !strings.Contains(string(body), "Native web_search is available") {
			t.Fatalf("web search declaration must enable native search: warnings=%v body=%s", bridge.Warnings, body)
		}
	}
	body, bridge, err := Prepare([]byte(`{"model":"gpt-6-astra","input":"hi","tools":[{"type":"function","name":"shell","parameters":{"type":"object"}},{"type":"image_generation"}]}`), "scope", nil)
	if err != nil || strings.Contains(string(body), "Native web_search is available") || len(bridge.Warnings) != 1 {
		t.Fatalf("image generation stays an omitted hosted tool: err=%v warnings=%v", err, bridge.Warnings)
	}
}
