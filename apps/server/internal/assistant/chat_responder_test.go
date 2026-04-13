package assistant

import (
	"strings"
	"testing"

	"agent_project/apps/server/internal/knowledge/citation"
	"agent_project/apps/server/internal/storage/postgres"

	"github.com/cloudwego/eino/schema"
)

func TestBuildChatMessagesMergesRuntimeContextIntoSingleSystemPrompt(t *testing.T) {
	messages := buildChatMessages(ChatCompletionInput{
		Citations: []citation.Citation{
			{
				SectionTitle: "考勤要求",
				Snippet:      "员工每天需要在 9 点前签到。",
			},
		},
		History: []postgres.AssistantMessage{
			{
				Role:    RoleUser,
				Kind:    KindText,
				Payload: mustJSON(t, TextPayload{Content: "先看这份制度"}),
			},
		},
		Message: "请总结考勤要求",
		Resource: &resourceContext{
			ID:     "resource-1",
			Title:  "学生手册",
			Source: "upload",
		},
	})

	if got := countAssistantTestMessagesByRole(messages, schema.System); got != 1 {
		t.Fatalf("expected exactly 1 system message, got %d", got)
	}

	if messages[0].Role != schema.System {
		t.Fatalf("expected first message to be system, got %q", messages[0].Role)
	}

	if !strings.Contains(messages[0].Content, "当前最近可用资源：标题=学生手册") {
		t.Fatalf("expected merged resource context in system prompt, got %q", messages[0].Content)
	}

	if !strings.Contains(messages[0].Content, "与本轮用户问题最相关的资源片段") {
		t.Fatalf("expected merged citations in system prompt, got %q", messages[0].Content)
	}

	if messages[len(messages)-1].Role != schema.User || messages[len(messages)-1].Content != "请总结考勤要求" {
		t.Fatalf("expected current user message to stay last, got %#v", messages[len(messages)-1])
	}
}

func countAssistantTestMessagesByRole(messages []*schema.Message, role schema.RoleType) int {
	count := 0
	for _, message := range messages {
		if message != nil && message.Role == role {
			count++
		}
	}

	return count
}
