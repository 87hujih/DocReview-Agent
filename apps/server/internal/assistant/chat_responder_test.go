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

func TestReplyJSONStreamExtractorHandlesSurrogatePair(t *testing.T) {
	extractor := &replyJSONStreamExtractor{}

	delta, err := extractor.Feed(`{"reply":"准备 \uD83D\uDE80 完成"}`)
	if err != nil {
		t.Fatalf("feed: %v", err)
	}

	if delta != "准备 🚀 完成" {
		t.Fatalf("expected decoded emoji reply, got %q", delta)
	}
	if extractor.Text() != "准备 🚀 完成" {
		t.Fatalf("expected buffered text to match, got %q", extractor.Text())
	}
}

func TestReplyJSONStreamExtractorHandlesSurrogatePairAcrossChunks(t *testing.T) {
	extractor := &replyJSONStreamExtractor{}
	chunks := []string{
		`{"reply":"准`,
		`备 \uD83`,
		`D\uDE`,
		`80 完成"}`,
	}

	var got strings.Builder
	for _, chunk := range chunks {
		delta, err := extractor.Feed(chunk)
		if err != nil {
			t.Fatalf("feed chunk %q: %v", chunk, err)
		}
		got.WriteString(delta)
	}

	if got.String() != "准备 🚀 完成" {
		t.Fatalf("expected decoded chunked emoji reply, got %q", got.String())
	}
	if extractor.Text() != got.String() {
		t.Fatalf("expected buffered text %q, got %q", got.String(), extractor.Text())
	}
}

func TestReplyJSONStreamExtractorRejectsInvalidSurrogatePair(t *testing.T) {
	cases := []string{
		`{"reply":"\uDE80"}`,
		`{"reply":"\uD83D\u0041"}`,
	}

	for _, input := range cases {
		extractor := &replyJSONStreamExtractor{}
		if _, err := extractor.Feed(input); err == nil {
			t.Fatalf("expected invalid surrogate error for %s", input)
		} else if !strings.Contains(err.Error(), "Unicode 代理项") {
			t.Fatalf("expected surrogate error, got %v", err)
		}
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
