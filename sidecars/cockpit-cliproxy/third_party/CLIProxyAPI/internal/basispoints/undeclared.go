package basispoints

import (
	"encoding/json"
	"log"
	"strings"
)

// maxUndeclaredToolName 是转交给客户端的未声明工具名的最大长度。
const maxUndeclaredToolName = 128

// undeclaredToolError 表示模型点名了本次请求没有声明的工具，并携带模型给出的名称与参数。
type undeclaredToolError struct {
	name    string
	payload any
}

func (e undeclaredToolError) Error() string {
	return "Basispoints returned a tool outside the client's catalog"
}

// undeclaredCall 暂存一次未声明工具调用，等整轮输出翻译完后再决定丢弃还是转交。
type undeclaredCall struct {
	native object
	call   undeclaredToolError
}

//// 模型已给出文字答复且没有其他工具调用时丢弃未声明调用，否则逐个转交客户端 [@x380kkm 2026-09-25] ////
func (b *Bridge) resolveUndeclaredCalls(items []any) ([]any, error) {
	declared := make([]any, 0, len(items))
	names := make([]string, 0)
	for _, raw := range items {
		if pending, isPending := raw.(undeclaredCall); isPending {
			names = append(names, loggableNativeToolName(strings.TrimPrefix(pending.call.name, hostToolPrefix)))
			continue
		}
		declared = append(declared, raw)
	}
	if len(names) == 0 {
		return items, nil
	}
	if hasAssistantText(declared) && !hasToolCalls(declared) {
		log.Printf("[Basispoints] stage=stream result=dropped_undeclared_tool count=%d tools=%s", len(names), strings.Join(names, ","))
		return declared, nil
	}
	resolved := make([]any, 0, len(items))
	for _, raw := range items {
		pending, isPending := raw.(undeclaredCall)
		if !isPending {
			resolved = append(resolved, raw)
			continue
		}
		relayed, err := b.relayUndeclaredCall(pending.native, pending.call)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, relayed)
	}
	log.Printf("[Basispoints] stage=stream result=relayed_undeclared_tool count=%d tools=%s", len(names), strings.Join(names, ","))
	return resolved, nil
}

// envelopePayload 取出传输信封里模型给出的参数，函数参数优先，其次是自定义工具输入。
func envelopePayload(envelope object) any {
	if arguments, err := envelopeArguments(envelope); err == nil && arguments != nil {
		return arguments
	}
	return envelope["input"]
}

//// 去掉宿主前缀后把未声明工具转成同名客户端调用，由客户端决定执行或回报不支持 [@x380kkm 2026-09-25] ////
func (b *Bridge) relayUndeclaredCall(native object, call undeclaredToolError) (object, error) {
	callID := text(native["call_id"])
	name := strings.TrimPrefix(call.name, hostToolPrefix)
	if callID == "" || name == "" || len(name) > maxUndeclaredToolName {
		return nil, call
	}
	arguments, isText := call.payload.(string)
	if !isText {
		if call.payload == nil {
			call.payload = object{}
		}
		encoded, err := json.Marshal(call.payload)
		if err != nil {
			return nil, call
		}
		arguments = string(encoded)
	}
	itemID := text(native["id"])
	if itemID == "" {
		itemID = "fc_" + fingerprint(callID)
	}
	result := object{"type": "function_call", "id": itemID, "call_id": callID, "name": name, "arguments": arguments, "status": "completed"}
	b.replay.put(b.scope, callID, native, result)
	return result, nil
}
