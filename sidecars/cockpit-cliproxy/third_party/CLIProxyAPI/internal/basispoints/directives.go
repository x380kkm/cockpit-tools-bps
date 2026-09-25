package basispoints

import (
	"encoding/json"
	"fmt"
	"strings"
)

//// 把 tool_choice 归一为 auto 或 none，强制选择改写为提示词指令 [@x380kkm 2026-09-25] ////
func normalizeToolChoice(value any) (string, string, error) {
	switch choice := value.(type) {
	case nil:
		return "auto", "", nil
	case string:
		switch strings.TrimSpace(choice) {
		case "", "auto":
			return "auto", "", nil
		case "none":
			return "none", "", nil
		case "required":
			return "auto", "You must call at least one client tool from the catalog in this response before answering.", nil
		}
		return "", "", fmt.Errorf("Basispoints does not support tool_choice %q", choice)
	case object:
		kind, name := strings.TrimSpace(text(choice["type"])), strings.TrimSpace(text(choice["name"]))
		switch {
		case (kind == "function" || kind == "custom") && name != "":
			if namespace := strings.TrimSpace(text(choice["namespace"])); namespace != "" {
				name = namespace + "." + name
			}
			return "auto", "You must call the client tool " + name + " in this response.", nil
		case strings.HasPrefix(kind, "web_search"):
			return "auto", "You must call native web_search in this response.", nil
		case kind == "allowed_tools":
			return "auto", "", nil
		}
		return "", "", fmt.Errorf("Basispoints cannot force the hosted tool_choice %q", kind)
	}
	return "", "", fmt.Errorf("Basispoints does not support this tool_choice value")
}

//// 把 text.format 或 response_format 的结构化输出要求改写为提示词指令 [@x380kkm 2026-09-25] ////
func structuredOutputDirective(source object) (string, error) {
	var format object
	if textConfig, ok := source["text"].(object); ok {
		format, _ = textConfig["format"].(object)
	}
	if format == nil {
		format, _ = source["response_format"].(object)
	}
	if format == nil {
		return "", nil
	}
	switch kind := text(format["type"]); kind {
	case "", "text":
		return "", nil
	case "json_object":
		return "Reply with exactly one valid JSON object and nothing else: no prose and no Markdown fences.", nil
	case "json_schema":
		schema := format["schema"]
		if nested, ok := format["json_schema"].(object); ok && schema == nil {
			schema = nested["schema"]
		}
		encoded, err := json.Marshal(schema)
		if err != nil {
			return "", fmt.Errorf("Basispoints structured output schema cannot be serialized")
		}
		return "Reply with exactly one JSON value that strictly conforms to this JSON Schema and nothing else: no prose and no Markdown fences.\nJSON Schema:\n" + string(encoded), nil
	default:
		return "", fmt.Errorf("Basispoints does not support structured output format %q", kind)
	}
}
