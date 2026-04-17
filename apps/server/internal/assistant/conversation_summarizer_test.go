package assistant

import (
	"context"
	"strings"
	"testing"

	"agent_project/apps/server/internal/agent/llmclient"
	"agent_project/apps/server/internal/storage/postgres"

	"github.com/cloudwego/eino/schema"
)

func TestConversationSummarizerSkipsEmptyTranscript(t *testing.T) {
	calls := 0
	summarizer := newConversationSummarizerWithClient(fakeAssistantLLMClient{
		generate: func(context.Context, []*schema.Message) (*schema.Message, error) {
			calls++
			return &schema.Message{Content: "不应被调用"}, nil
		},
	}, llmclient.Config{TimeoutMS: 1000})

	result, err := summarizer.Summarize(context.Background(), SummaryInput{})
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result for empty transcript, got %#v", result)
	}
	if calls != 0 {
		t.Fatalf("expected summarizer client to be skipped, got %d calls", calls)
	}
}

func TestConversationSummarizerBuildsSummaryFromOldSummaryAndTranscript(t *testing.T) {
	var captured []*schema.Message
	summarizer := newConversationSummarizerWithClient(fakeAssistantLLMClient{
		generate: func(_ context.Context, messages []*schema.Message) (*schema.Message, error) {
			captured = messages
			return &schema.Message{
				Content: "当前目标：继续优化第二章。\n关键结论：保留按天登记。\n待继续事项：比较两个改写方案。",
			}, nil
		},
	}, llmclient.Config{TimeoutMS: 1000})

	oldSummary := "当前目标：先梳理学生手册第二章。"
	result, err := summarizer.Summarize(context.Background(), SummaryInput{
		PreviousSummary: &oldSummary,
		Snapshot: &SessionContextSnapshot{
			ActiveResource: &SnapshotActiveResource{
				ID:         "resource-1",
				Title:      "第二版学生手册",
				SourceType: "upload",
			},
		},
		Transcript: []postgres.AssistantMessage{
			{
				Role:    RoleUser,
				Kind:    KindText,
				Payload: mustJSON(t, TextPayload{Content: "继续优化第二章"}),
			},
			{
				Role:    RoleAssistant,
				Kind:    KindText,
				Payload: mustJSON(t, TextPayload{Content: "先保留按天登记和节假日例外。"}),
			},
		},
	})
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}

	if result == nil || result.Summary != "当前目标：继续优化第二章。\n关键结论：保留按天登记。\n待继续事项：比较两个改写方案。" {
		t.Fatalf("expected returned summary to be trimmed llm content, got %#v", result)
	}
	if len(captured) != 2 {
		t.Fatalf("expected summarizer to send 2 messages, got %d", len(captured))
	}
	if !strings.Contains(captured[1].Content, "已有摘要：\n当前目标：先梳理学生手册第二章。") {
		t.Fatalf("expected summary prompt to include previous summary, got %q", captured[1].Content)
	}
	if !strings.Contains(captured[1].Content, "用户：继续优化第二章") {
		t.Fatalf("expected summary prompt to include user transcript, got %q", captured[1].Content)
	}
	if !strings.Contains(captured[1].Content, "助手：先保留按天登记和节假日例外。") {
		t.Fatalf("expected summary prompt to include assistant transcript, got %q", captured[1].Content)
	}
}

func TestConversationSummarizerPreservesStableSnapshotContextWithoutDuplicatingIt(t *testing.T) {
	var captured []*schema.Message
	summarizer := newConversationSummarizerWithClient(fakeAssistantLLMClient{
		generate: func(_ context.Context, messages []*schema.Message) (*schema.Message, error) {
			captured = messages
			return &schema.Message{
				Content: "当前目标：继续优化第二章。\n关键结论：活跃资源保持第二版学生手册。\n待继续事项：等待创建正式任务。",
			}, nil
		},
	}, llmclient.Config{TimeoutMS: 1000})

	_, err := summarizer.Summarize(context.Background(), SummaryInput{
		Snapshot: &SessionContextSnapshot{
			ActiveResource: &SnapshotActiveResource{
				ID:         "resource-2",
				Title:      "第二版学生手册",
				SourceType: "upload",
			},
			PendingTaskSuggestion: &SnapshotPendingTaskSuggestion{
				MessageID:   "suggestion-1",
				Instruction: "请整理第二章为正式修订任务。",
			},
			LatestTask: &SnapshotLatestTask{
				ID:     "task-1",
				Status: "planning",
			},
		},
		Transcript: []postgres.AssistantMessage{
			{
				Role:    RoleAssistant,
				Kind:    KindSessionFile,
				Payload: mustJSON(t, SessionFilePayload{FileName: "students-v2.md", ResourceID: "resource-2", ResourceTitle: "第二版学生手册", SourceType: "upload", Status: "ready"}),
			},
			{
				Role:    RoleUser,
				Kind:    KindText,
				Payload: mustJSON(t, TextPayload{Content: "继续优化第二章"}),
			},
		},
	})
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}

	if len(captured) != 2 {
		t.Fatalf("expected summarizer to send 2 messages, got %d", len(captured))
	}
	prompt := captured[1].Content
	if !strings.Contains(prompt, "当前活跃资源：第二版学生手册（来源=upload，资源ID=resource-2）") {
		t.Fatalf("expected summary prompt to preserve active resource context, got %q", prompt)
	}
	if !strings.Contains(prompt, "待确认任务：请整理第二章为正式修订任务。") {
		t.Fatalf("expected summary prompt to preserve pending task context, got %q", prompt)
	}
	if !strings.Contains(prompt, "最近任务状态：ID=task-1，状态=planning") {
		t.Fatalf("expected summary prompt to preserve latest task context, got %q", prompt)
	}
	if strings.Contains(prompt, "students-v2.md") {
		t.Fatalf("expected summary prompt to drop structured transcript messages, got %q", prompt)
	}
}
