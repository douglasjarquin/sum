package hcclient

import (
	"errors"
	"strings"
)

const AgentPromptStalled = "agent_prompt_stalled"

func AgentPromptStalledError(err error) bool {
	if err == nil {
		return false
	}
	var rpc *RPCError
	if errors.As(err, &rpc) && (strings.Contains(rpc.Message, AgentPromptStalled) || strings.Contains(string(rpc.Data), AgentPromptStalled)) {
		return true
	}
	return strings.Contains(err.Error(), AgentPromptStalled)
}

func AgentPromptStalledReason(reason *string) bool {
	return reason != nil && strings.Contains(*reason, AgentPromptStalled)
}
