package basispoints

import (
	"fmt"
	"strings"
)

// fernetCiphertextPrefix 是 OpenAI 加密 agent 消息的 Fernet 令牌开头。
const fernetCiphertextPrefix = "gAAAAA"

//// 把 Codex 多 agent 消息改写为用户消息，明文 encrypted_content 按文本保留 [@x380kkm 2026-09-25] ////
func agentMessageAsUserMessage(item object) (object, error) {
	parts, _ := item["content"].([]any)
	lines := make([]string, 0, len(parts)+1)
	if author, recipient := text(item["author"]), text(item["recipient"]); author != "" || recipient != "" {
		lines = append(lines, fmt.Sprintf("[agent message from %s to %s]", author, recipient))
	}
	for _, raw := range parts {
		part, _ := raw.(object)
		switch kind := text(part["type"]); kind {
		case "input_text", "output_text", "text":
			lines = append(lines, text(part["text"]))
		case "encrypted_content":
			value := text(part["encrypted_content"])
			if strings.HasPrefix(value, fernetCiphertextPrefix) {
				return nil, fmt.Errorf("Basispoints cannot read an encrypted agent message; enable multi-agent v2 optimization so agent messages arrive as plain text")
			}
			lines = append(lines, value)
		default:
			return nil, fmt.Errorf("Basispoints does not support agent message content %q", kind)
		}
	}
	return object{"type": "message", "role": "user", "content": []any{object{"type": "input_text", "text": strings.Join(lines, "\n")}}}, nil
}
