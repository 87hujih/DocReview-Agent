package assistant

import (
	"context"
	"strings"
	"testing"
	"time"

	"agent_project/apps/server/internal/storage/postgres"
)

// TestReplyAuditRejectsSnippetOnlyWordingWhenCanonicalAccessSucceeded 验证当前文件已可见时会拦截旧的 snippet-only 话术。
func TestReplyAuditRejectsSnippetOnlyWordingWhenCanonicalAccessSucceeded(t *testing.T) {
	sanitized, rewritten := AuditReply(ReplyAuditInput{
		CurrentDocumentReady: true,
		CanonicalAccessOK:    true,
		Reply:                "我只能基于你提供的文档片段先做判断，如果有完整文档我可以继续分析。",
	})

	if !rewritten {
		t.Fatal("expected reply audit to rewrite stale wording")
	}
	if strings.Contains(sanitized, "文档片段") || strings.Contains(sanitized, "如果有完整文档") {
		t.Fatalf("expected sanitized reply to remove stale wording, got %q", sanitized)
	}
}

// TestReplyAuditAllowsExplicitFailureWhenCurrentDocumentUnavailable 验证当前文件不可见时不会误改原始回复。
func TestReplyAuditAllowsExplicitFailureWhenCurrentDocumentUnavailable(t *testing.T) {
	original := "如果你有完整文档，我可以继续分析。"
	sanitized, rewritten := AuditReply(ReplyAuditInput{
		CurrentDocumentReady: false,
		CanonicalAccessOK:    false,
		Reply:                original,
	})

	if rewritten {
		t.Fatal("expected reply audit to keep original reply")
	}
	if sanitized != original {
		t.Fatalf("expected original reply to stay unchanged, got %q", sanitized)
	}
}

// TestReplyAuditBlocksFalseModificationClaimWithoutExecution 验证没有真实副作用时，reply audit 会拦截“已经修改好了”。
func TestReplyAuditBlocksFalseModificationClaimWithoutExecution(t *testing.T) {
	sanitized, rewritten := AuditReply(ReplyAuditInput{
		OutcomeKind: string(AssistantOutcomeChatAnswer),
		Reply:       "我已经修改好了，你可以直接查看结果。",
	})

	if !rewritten {
		t.Fatal("expected reply audit to rewrite false modification claim")
	}
	if strings.Contains(sanitized, "已经修改好了") {
		t.Fatalf("expected sanitized reply to remove false completion claim, got %q", sanitized)
	}
	if !strings.Contains(sanitized, "还没有真正修改文档") {
		t.Fatalf("expected sanitized reply to explain no real side effect happened, got %q", sanitized)
	}
}

// TestReplyAuditBlocksFalseTaskCreationClaimWithoutExecution 验证没有真实副作用时，reply audit 会拦截“已经创建任务”。
func TestReplyAuditBlocksFalseTaskCreationClaimWithoutExecution(t *testing.T) {
	sanitized, rewritten := AuditReply(ReplyAuditInput{
		OutcomeKind: string(AssistantOutcomeChatAnswer),
		Reply:       "我已经创建任务了，接下来会继续执行。",
	})

	if !rewritten {
		t.Fatal("expected reply audit to rewrite false task creation claim")
	}
	if strings.Contains(sanitized, "已经创建任务") {
		t.Fatalf("expected sanitized reply to remove false task creation claim, got %q", sanitized)
	}
	if !strings.Contains(sanitized, "还没有真正修改文档") {
		t.Fatalf("expected sanitized reply to explain no real side effect happened, got %q", sanitized)
	}
}

// TestReplyAuditRewritesWorkflowProposalCompletionClaim 验证 workflow proposal 阶段不会放行“已修改完成”之类伪完成话术。
func TestReplyAuditRewritesWorkflowProposalCompletionClaim(t *testing.T) {
	sanitized, rewritten := AuditReply(ReplyAuditInput{
		OutcomeKind:          string(AssistantOutcomeWorkflowProposal),
		Reply:                "我已经完成了修改，下面给你任务卡。",
	})

	if !rewritten {
		t.Fatal("expected workflow proposal audit to rewrite completion claim")
	}
	if strings.Contains(sanitized, "完成了修改") || strings.Contains(sanitized, "任务卡") {
		t.Fatalf("expected workflow proposal audit to remove completion claim, got %q", sanitized)
	}
	if !strings.Contains(sanitized, "尚未写回原文件") {
		t.Fatalf("expected workflow proposal audit to keep proposal-phase wording, got %q", sanitized)
	}
}

// TestAppendMessageRewritesInvalidSnippetOnlyReplyInFileAwareMode 验证非流式回复在 file-aware 模式下会改写旧话术。
func TestAppendMessageRewritesInvalidSnippetOnlyReplyInFileAwareMode(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("当前文件分析")
	repo.seedMessage(session.ID, postgres.AssistantMessage{
		ID:         "message-file",
		SessionID:  session.ID,
		Role:       RoleAssistant,
		Kind:       KindSessionFile,
		SequenceNo: 1,
		Payload: mustJSON(t, SessionFilePayload{
			FileName:      "resume.md",
			ResourceID:    "resource-1",
			ResourceTitle: "产品经理简历",
			SourceType:    "upload",
			Status:        "ready",
		}),
		CreatedAt: time.Now(),
	})
	currentFileReader := &fakeCurrentFileSectionReader{
		currentVersion: &postgres.ResourceVersion{
			ID:         "version-1",
			ResourceID: "resource-1",
			Content:    "整份简历正文",
		},
		allSections: []postgres.ResourceSection{
			{ID: "section-3", ResourceID: "resource-1", VersionID: "version-1", SectionType: "project", SectionOrder: 3, Title: "慢跑计划", Content: "这是第三个项目的完整正文"},
		},
	}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{
			result: &ChatCompletionResult{
				Reply: "我只能基于你提供的文档片段先做判断，如果有完整文档我可以继续分析。",
			},
		},
		nil,
		WithCurrentDocumentLoader(NewCurrentDocumentLoader(currentFileReader)),
		WithSectionLocator(NewSectionLocator(currentFileReader)),
		WithSectionReader(NewSectionReader(currentFileReader)),
	)

	result, err := service.AppendMessage(context.Background(), session.ID, "结合我刚上传的简历，详细分析第三个项目的问题")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}

	reply := decodeTextPayload(t, result.Messages[1].Payload)
	if strings.Contains(reply.Content, "文档片段") || strings.Contains(reply.Content, "如果有完整文档") {
		t.Fatalf("expected rewritten reply, got %q", reply.Content)
	}
}

// TestAppendMessageStreamRewritesInvalidSnippetOnlyReplyInFileAwareMode 验证流式回复在 file-aware 模式下也会改写旧话术。
func TestAppendMessageStreamRewritesInvalidSnippetOnlyReplyInFileAwareMode(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("当前文件分析")
	repo.seedMessage(session.ID, postgres.AssistantMessage{
		ID:         "message-file",
		SessionID:  session.ID,
		Role:       RoleAssistant,
		Kind:       KindSessionFile,
		SequenceNo: 1,
		Payload: mustJSON(t, SessionFilePayload{
			FileName:      "resume.md",
			ResourceID:    "resource-1",
			ResourceTitle: "产品经理简历",
			SourceType:    "upload",
			Status:        "ready",
		}),
		CreatedAt: time.Now(),
	})
	currentFileReader := &fakeCurrentFileSectionReader{
		currentVersion: &postgres.ResourceVersion{
			ID:         "version-1",
			ResourceID: "resource-1",
			Content:    "整份简历正文",
		},
		allSections: []postgres.ResourceSection{
			{ID: "section-3", ResourceID: "resource-1", VersionID: "version-1", SectionType: "project", SectionOrder: 3, Title: "慢跑计划", Content: "这是第三个项目的完整正文"},
		},
	}
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{
			stream: &fakeChatStream{
				chunks: []string{"我只能基于你提供的文档片段先做判断，如果有完整文档我可以继续分析。"},
			},
		},
		nil,
		WithCurrentDocumentLoader(NewCurrentDocumentLoader(currentFileReader)),
		WithSectionLocator(NewSectionLocator(currentFileReader)),
		WithSectionReader(NewSectionReader(currentFileReader)),
	)

	if err := service.AppendMessageStream(context.Background(), session.ID, "结合我刚上传的简历，详细分析第三个项目的问题", func(StreamEvent) error { return nil }); err != nil {
		t.Fatalf("append message stream: %v", err)
	}

	messages, err := repo.ListMessages(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) < 2 {
		t.Fatalf("expected persisted assistant reply, got %d messages", len(messages))
	}
	reply := decodeTextPayload(t, messages[len(messages)-1].Payload)
	if strings.Contains(reply.Content, "文档片段") || strings.Contains(reply.Content, "如果有完整文档") {
		t.Fatalf("expected rewritten stream reply, got %q", reply.Content)
	}
}

// TestAppendMessageRewritesFalseCompletionClaimWithoutExecution 验证非流式 generic reply 也会拦截伪完成态。
func TestAppendMessageRewritesFalseCompletionClaimWithoutExecution(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("通用顾问对话")
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{
			result: &ChatCompletionResult{
				Reply: "我已经完成了修改，你现在可以直接查看。",
			},
		},
		nil,
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "analysis",
				ResponseMode:        ResponseModeAnswerOnly,
				ConversationMode:    "advise",
				RequestedNextStep:   "answer",
				ChatFulfillable:     true,
				EvidenceSufficiency: "sufficient",
				Confidence:          0.8,
				Reasons:             []string{"当前只是顾问态回复"},
			},
		}),
	)

	result, err := service.AppendMessage(context.Background(), session.ID, "先说说第三个项目该怎么改")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}

	reply := decodeTextPayload(t, result.Messages[1].Payload)
	if strings.Contains(reply.Content, "完成了修改") {
		t.Fatalf("expected false completion claim to be removed, got %q", reply.Content)
	}
	if !strings.Contains(reply.Content, "还没有真正修改文档") {
		t.Fatalf("expected rewritten reply to explain no real side effect happened, got %q", reply.Content)
	}
}

// TestAppendMessageStreamRewritesFalseTaskCreationClaimWithoutExecution 验证流式 generic reply 也会拦截伪创建态。
func TestAppendMessageStreamRewritesFalseTaskCreationClaimWithoutExecution(t *testing.T) {
	repo := newFakeSessionRepo()
	session := repo.seedSession("通用顾问对话")
	service := NewService(
		repo,
		&fakeDocumentImporter{},
		&fakeTaskCreator{},
		&fakeChatResponder{
			stream: &fakeChatStream{
				chunks: []string{"我已经创建任务了，接下来会继续执行。"},
				result: &ChatCompletionResult{Reply: "我已经创建任务了，接下来会继续执行。"},
			},
		},
		nil,
		WithDeliberationAgent(&fakeDeliberationAgent{
			result: &DeliberationDecision{
				RequestKind:         "analysis",
				ResponseMode:        ResponseModeAnswerOnly,
				ConversationMode:    "advise",
				RequestedNextStep:   "answer",
				ChatFulfillable:     true,
				EvidenceSufficiency: "sufficient",
				Confidence:          0.8,
				Reasons:             []string{"当前只是顾问态回复"},
			},
		}),
	)

	if err := service.AppendMessageStream(context.Background(), session.ID, "先帮我分析一下第三个项目", func(StreamEvent) error { return nil }); err != nil {
		t.Fatalf("append message stream: %v", err)
	}

	messages, err := repo.ListMessages(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	reply := decodeTextPayload(t, messages[len(messages)-1].Payload)
	if strings.Contains(reply.Content, "已经创建任务") {
		t.Fatalf("expected false task creation claim to be removed, got %q", reply.Content)
	}
	if !strings.Contains(reply.Content, "还没有真正修改文档") {
		t.Fatalf("expected rewritten reply to explain no real side effect happened, got %q", reply.Content)
	}
}
