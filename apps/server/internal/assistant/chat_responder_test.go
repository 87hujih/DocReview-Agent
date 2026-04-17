package assistant

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"agent_project/apps/server/internal/agent/llmclient"
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

	if !strings.Contains(messages[0].Content, "本轮检索所用资源：标题=学生手册") {
		t.Fatalf("expected merged resource context in system prompt, got %q", messages[0].Content)
	}

	if !strings.Contains(messages[0].Content, "与本轮用户问题最相关的资源片段") {
		t.Fatalf("expected merged citations in system prompt, got %q", messages[0].Content)
	}

	if messages[len(messages)-1].Role != schema.User || messages[len(messages)-1].Content != "请总结考勤要求" {
		t.Fatalf("expected current user message to stay last, got %#v", messages[len(messages)-1])
	}
}

func TestReplyParsesOptionalTaskInstruction(t *testing.T) {
	responder := newChatResponderWithClient(fakeAssistantLLMClient{
		generate: func(_ context.Context, _ []*schema.Message) (*schema.Message, error) {
			return &schema.Message{Content: `{"reply":"可以开始。","task_instruction":"请把这份简历改成产品经理版本"}`}, nil
		},
	}, llmclient.Config{TimeoutMS: 1400})

	result, err := responder.Reply(context.Background(), ChatCompletionInput{Message: "请直接开始"})
	if err != nil {
		t.Fatalf("reply: %v", err)
	}

	if result.Reply != "可以开始。" {
		t.Fatalf("expected reply %q, got %q", "可以开始。", result.Reply)
	}
	if result.TaskInstruction == nil || *result.TaskInstruction != "请把这份简历改成产品经理版本" {
		t.Fatalf("expected optional task instruction to be parsed, got %#v", result.TaskInstruction)
	}
}

func TestBuildChatMessagesPromptMentionsTaskInstructionIsOptionalOnlyWhenReady(t *testing.T) {
	readyMessages := buildChatMessages(ChatCompletionInput{
		Message: "请直接把这份简历改成产品经理版本",
		TaskSuggestionDecision: &TaskSuggestionDecision{
			ReadinessState: ReadinessStateReadyForTask,
		},
	})
	readyPrompt := readyMessages[0].Content
	if !strings.Contains(readyPrompt, `"task_instruction":"仅当用户要求立即开始执行且材料已明确时才填写，可选"`) {
		t.Fatalf("expected ready prompt to mention optional task_instruction, got %q", readyPrompt)
	}

	notReadyMessages := buildChatMessages(ChatCompletionInput{
		Message: "这份简历还有什么需要优化的吗",
		TaskSuggestionDecision: &TaskSuggestionDecision{
			ReadinessState: ReadinessStateReadyButNotExecuting,
		},
	})
	notReadyPrompt := notReadyMessages[0].Content
	if strings.Contains(notReadyPrompt, "task_instruction") {
		t.Fatalf("expected non-ready prompt to avoid task_instruction hint, got %q", notReadyPrompt)
	}
}

func TestBuildChatMessagesIncludesSnapshotProjection(t *testing.T) {
	messages := buildChatMessages(ChatCompletionInput{
		Snapshot: &SessionContextSnapshot{
			ActiveResource: &SnapshotActiveResource{
				ID:         "resource-snapshot",
				Title:      "第二版学生手册",
				SourceType: "upload",
			},
			PendingTaskSuggestion: &SnapshotPendingTaskSuggestion{
				MessageID:   "message-suggestion",
				Instruction: "请整理第二章为执行任务",
			},
			LatestTask: &SnapshotLatestTask{
				ID:     "task-2",
				Status: "executing",
			},
			ConfirmedConstraints: []ConfirmedConstraint{
				{Label: "输出格式", Value: "表格"},
			},
		},
		Citations: []citation.Citation{
			{
				SectionTitle: "第二章",
				Snippet:      "第二版学生手册强调考勤按天登记。",
			},
		},
		History: []postgres.AssistantMessage{
			{
				Role: RoleAssistant,
				Kind: KindSessionFile,
				Payload: mustJSON(t, SessionFilePayload{
					FileName:      "旧学生手册.md",
					ResourceID:    "resource-history",
					ResourceTitle: "旧学生手册",
					SourceType:    "upload",
					Status:        "ready",
				}),
			},
			{
				Role: RoleAssistant,
				Kind: KindTaskCreated,
				Payload: mustJSON(t, TaskCreatedPayload{
					Instruction:         "旧任务",
					ResourceID:          "resource-history",
					Status:              "pending",
					SuggestionMessageID: "message-history-suggestion",
					TaskID:              "task-history",
				}),
			},
		},
		Message: "继续总结第二章",
		Resource: &resourceContext{
			ID:     "resource-snapshot",
			Title:  "第二版学生手册",
			Source: "upload",
		},
	})

	if got := countAssistantTestMessagesByRole(messages, schema.System); got != 1 {
		t.Fatalf("expected exactly 1 system message, got %d", got)
	}

	systemPrompt := messages[0].Content
	if !strings.Contains(systemPrompt, "当前会话快照") {
		t.Fatalf("expected snapshot projection in system prompt, got %q", systemPrompt)
	}
	if !strings.Contains(systemPrompt, "当前活跃资源：第二版学生手册") {
		t.Fatalf("expected snapshot active resource in system prompt, got %q", systemPrompt)
	}
	if !strings.Contains(systemPrompt, "待确认任务建议：请整理第二章为执行任务") {
		t.Fatalf("expected pending suggestion in system prompt, got %q", systemPrompt)
	}
	if !strings.Contains(systemPrompt, "最近真实任务：ID=task-2；状态=executing") {
		t.Fatalf("expected latest task snapshot in system prompt, got %q", systemPrompt)
	}
	if !strings.Contains(systemPrompt, "已确认约束：输出格式=表格") {
		t.Fatalf("expected confirmed constraints in system prompt, got %q", systemPrompt)
	}
	if strings.Contains(systemPrompt, "旧学生手册") {
		t.Fatalf("expected system prompt to prefer snapshot over historical resource text, got %q", systemPrompt)
	}
}

func TestBuildChatMessagesIncludesRollingSummaryBeforeRecentTurns(t *testing.T) {
	summary := "当前目标：继续优化第二章。\n关键结论：保留按天登记。\n待继续事项：比较两个改写方案。"
	messages := buildChatMessages(ChatCompletionInput{
		Snapshot: &SessionContextSnapshot{
			RollingSummary: &summary,
		},
		History: []postgres.AssistantMessage{
			{
				Role:    RoleUser,
				Kind:    KindText,
				Payload: mustJSON(t, TextPayload{Content: "先看第二章"}),
			},
			{
				Role:    RoleAssistant,
				Kind:    KindText,
				Payload: mustJSON(t, TextPayload{Content: "好的，我先整理。"}),
			},
		},
		Message: "继续",
	})

	if !strings.Contains(messages[0].Content, "当前会话滚动摘要：\n"+summary) {
		t.Fatalf("expected system prompt to include rolling summary, got %q", messages[0].Content)
	}
	if len(messages) < 4 {
		t.Fatalf("expected rolling summary prompt to keep recent turns, got %d messages", len(messages))
	}
	if messages[1].Content != "先看第二章" || messages[2].Content != "好的，我先整理。" {
		t.Fatalf("expected recent text turns to stay after system prompt, got %#v", messages[1:3])
	}
}

func TestBuildHistoryMessagesDropsStructuredMessagesFromRecentWindow(t *testing.T) {
	history := []postgres.AssistantMessage{
		{
			Role:    RoleAssistant,
			Kind:    KindSessionFile,
			Payload: mustJSON(t, SessionFilePayload{FileName: "students.md", ResourceID: "resource-1", ResourceTitle: "学生手册", SourceType: "upload", Status: "ready"}),
		},
		{
			Role:    RoleUser,
			Kind:    KindText,
			Payload: mustJSON(t, TextPayload{Content: "先看第二章"}),
		},
		{
			Role:    RoleAssistant,
			Kind:    KindTaskSuggestion,
			Payload: []byte(`{"instruction":"请整理第二章","status_message":"资源已明确"}`),
		},
		{
			Role:    RoleAssistant,
			Kind:    KindText,
			Payload: mustJSON(t, TextPayload{Content: "我先保留考勤规则。"}),
		},
	}

	messages := buildHistoryMessages(history)
	if len(messages) != 2 {
		t.Fatalf("expected only 2 text history messages, got %d", len(messages))
	}
	if messages[0].Content != "先看第二章" || messages[1].Content != "我先保留考勤规则。" {
		t.Fatalf("expected structured history to be dropped, got %#v", messages)
	}
}

func TestBuildHistoryMessagesRespectsRecentTurnBudget(t *testing.T) {
	countLimitedHistory := make([]postgres.AssistantMessage, 0, 9)
	for index := 1; index <= 9; index++ {
		countLimitedHistory = append(countLimitedHistory, postgres.AssistantMessage{
			Role:    RoleUser,
			Kind:    KindText,
			Payload: mustJSON(t, TextPayload{Content: fmt.Sprintf("message-%02d", index)}),
		})
	}

	countLimited := buildHistoryMessages(countLimitedHistory)
	if len(countLimited) != 8 {
		t.Fatalf("expected recent turn budget to keep last 8 text messages, got %d", len(countLimited))
	}
	if countLimited[0].Content != "message-02" || countLimited[len(countLimited)-1].Content != "message-09" {
		t.Fatalf("expected count budget to keep newest 8 messages, got first=%q last=%q", countLimited[0].Content, countLimited[len(countLimited)-1].Content)
	}

	longText := strings.Repeat("甲", 900)
	charLimited := buildHistoryMessages([]postgres.AssistantMessage{
		{Role: RoleUser, Kind: KindText, Payload: mustJSON(t, TextPayload{Content: longText})},
		{Role: RoleAssistant, Kind: KindText, Payload: mustJSON(t, TextPayload{Content: longText})},
		{Role: RoleUser, Kind: KindText, Payload: mustJSON(t, TextPayload{Content: longText})},
	})
	if len(charLimited) != 1 {
		t.Fatalf("expected char budget to keep only the newest message, got %d", len(charLimited))
	}
	if charLimited[0].Content != longText {
		t.Fatalf("expected char-limited history to keep latest text only")
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

func TestChatResponderReplyUsesConfiguredTimeoutAndRetries(t *testing.T) {
	var remaining []time.Duration
	attempts := 0

	responder := newChatResponderWithClient(fakeAssistantLLMClient{
		generate: func(ctx context.Context, _ []*schema.Message) (*schema.Message, error) {
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatal("expected reply context deadline")
			}

			remaining = append(remaining, time.Until(deadline))
			attempts++
			if attempts == 1 {
				return nil, context.DeadlineExceeded
			}

			return &schema.Message{Content: `{"reply":"你好"}`}, nil
		},
	}, llmclient.Config{
		TimeoutMS: 1400,
		RetryMax:  1,
		BackoffMS: 1,
	})

	result, err := responder.Reply(context.Background(), ChatCompletionInput{Message: "你好"})
	if err != nil {
		t.Fatalf("reply: %v", err)
	}

	if result.Reply != "你好" {
		t.Fatalf("expected reply %q, got %q", "你好", result.Reply)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 generate attempts, got %d", attempts)
	}
	if len(remaining) != 2 {
		t.Fatalf("expected 2 recorded deadlines, got %d", len(remaining))
	}

	for index, duration := range remaining {
		if duration <= 0 || duration > 2*time.Second {
			t.Fatalf("expected attempt %d timeout near configured value, got %s", index+1, duration)
		}
	}
}

func TestChatResponderStreamRetriesOpenWithoutInjectingHardDeadline(t *testing.T) {
	attempts := 0
	var sawDeadline []bool

	responder := newChatResponderWithClient(fakeAssistantLLMClient{
		stream: func(ctx context.Context, _ []*schema.Message) (assistantLLMStream, error) {
			_, ok := ctx.Deadline()
			sawDeadline = append(sawDeadline, ok)
			attempts++
			if attempts == 1 {
				return nil, fmt.Errorf("stream open failed: %w", context.DeadlineExceeded)
			}

			return &fakeAssistantLLMStream{
				messages: []*schema.Message{
					{Content: `{"reply":"你好"}`},
				},
			}, nil
		},
	}, llmclient.Config{
		TimeoutMS: 90000,
		RetryMax:  1,
		BackoffMS: 1,
	})

	stream, err := responder.Stream(context.Background(), ChatCompletionInput{Message: "你好"})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	delta, err := stream.Recv()
	if err != nil {
		t.Fatalf("recv first chunk: %v", err)
	}
	if delta != "你好" {
		t.Fatalf("expected stream delta %q, got %q", "你好", delta)
	}

	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF after first chunk, got %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 stream open attempts, got %d", attempts)
	}
	if len(sawDeadline) != 2 {
		t.Fatalf("expected 2 recorded stream attempts, got %d", len(sawDeadline))
	}
	for index, ok := range sawDeadline {
		if ok {
			t.Fatalf("expected stream attempt %d to avoid injected deadline", index+1)
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

type fakeAssistantLLMClient struct {
	generate func(ctx context.Context, messages []*schema.Message) (*schema.Message, error)
	stream   func(ctx context.Context, messages []*schema.Message) (assistantLLMStream, error)
}

func (f fakeAssistantLLMClient) Generate(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
	if f.generate == nil {
		return nil, errors.New("generate not configured")
	}

	return f.generate(ctx, messages)
}

func (f fakeAssistantLLMClient) Stream(ctx context.Context, messages []*schema.Message) (assistantLLMStream, error) {
	if f.stream == nil {
		return nil, errors.New("stream not configured")
	}

	return f.stream(ctx, messages)
}

type fakeAssistantLLMStream struct {
	index    int
	messages []*schema.Message
}

func (f *fakeAssistantLLMStream) Recv() (*schema.Message, error) {
	if f.index >= len(f.messages) {
		return nil, io.EOF
	}

	message := f.messages[f.index]
	f.index++
	return message, nil
}

func (f *fakeAssistantLLMStream) Close() {}
