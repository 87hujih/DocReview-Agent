package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"strings"
	"testing"
	"time"

	executoragent "agent_project/apps/server/internal/agent/executor"
	"agent_project/apps/server/internal/approval"
	"agent_project/apps/server/internal/assistant"
	appconfig "agent_project/apps/server/internal/config"
	documentparser "agent_project/apps/server/internal/document/parser"
	"agent_project/apps/server/internal/job"
	"agent_project/apps/server/internal/knowledge/indexer"
	"agent_project/apps/server/internal/knowledge/ingest"
	"agent_project/apps/server/internal/storage/filestore"
	"agent_project/apps/server/internal/storage/postgres"
	taskevents "agent_project/apps/server/internal/task/events"
	"agent_project/apps/server/internal/task/models"
	taskservice "agent_project/apps/server/internal/task/service"
	"agent_project/apps/server/internal/testsupport/postgrescleanup"
	"agent_project/apps/server/internal/testsupport/postgrestest"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestUploadApproveExecuteAndExportFlow(t *testing.T) {
	pool := newFlowTestPool(t)
	ctx := flowTestContext(t)

	resourceRepo := postgres.NewResourceRepo(pool)
	assistantRepo := postgres.NewAssistantRepo(pool)
	uploadedFileRepo := postgres.NewUploadedFileRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	approvalRepo := postgres.NewApprovalRepo(pool)
	jobRepo := postgres.NewJobRepo(pool)
	eventRepo := postgres.NewTaskEventRepo(pool)

	store, err := filestore.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("create local file store: %v", err)
	}
	parser, err := documentparser.New(documentparser.Options{Mode: documentparser.ModeText})
	if err != nil {
		t.Fatalf("create parser: %v", err)
	}

	ingestService := ingest.NewService(resourceRepo, flowEmbedder{}, ingest.WithParser(parser))
	assistantService := assistant.NewService(
		assistantRepo,
		assistant.NewIngestDocumentImporter(ingestService),
		nil,
		nil,
		nil,
		assistant.WithUploadedFileStorage(store, uploadedFileRepo),
	)
	assistantHandler := NewAssistantHandler(assistantService)

	session, _, err := assistantRepo.CreateSessionWithMessages(ctx, "端到端 flow", []postgres.AssistantMessageInput{
		{
			Role:    assistant.RoleUser,
			Kind:    assistant.KindText,
			Payload: []byte(`{"content":"请修订这份学生守则"}`),
		},
	})
	if err != nil {
		t.Fatalf("create assistant session: %v", err)
	}

	uploadResponse := uploadFlowFile(t, assistantHandler, session.ID, "学生守则.md", []byte(strings.Join([]string{
		"# 学生守则",
		"",
		"## 第一章",
		"原始第一章内容",
		"",
	}, "\n")))
	if uploadResponse.Resource == nil {
		t.Fatal("expected uploaded file to create a resource")
	}
	resourceID := uploadResponse.Resource.ID
	t.Cleanup(func() {
		cleanupFlowData(t, pool, resourceID, session.ID, uploadResponse.FileID)
	})

	if uploadResponse.FileID == "" {
		t.Fatal("expected session_file payload to include original file id")
	}
	downloadOriginalBody := downloadFlowFile(t, uploadedFileRepo, store, uploadResponse.FileID)
	if !strings.Contains(downloadOriginalBody, "# 学生守则") {
		t.Fatalf("expected original file download body, got %q", downloadOriginalBody)
	}

	taskService := taskservice.New(taskRepo, resourceRepo, nil, taskevents.New(eventRepo))
	task, err := taskService.CreateTask(ctx, resourceID, "把第一章改成最终修订内容")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := taskRepo.AddArtifact(ctx, task.ID, "diff_preview", []byte(`{"sections":[{"section_title":"第一章","original":"原始第一章内容","revised":"最终修订内容","reason":"验证端到端闭环","citation_ids":["cite_1"]}]}`)); err != nil {
		t.Fatalf("add diff preview artifact: %v", err)
	}
	currentVersion, err := resourceRepo.GetCurrentVersion(ctx, resourceID)
	if err != nil {
		t.Fatalf("get current version: %v", err)
	}
	if currentVersion == nil {
		t.Fatal("expected uploaded resource to have current version")
	}
	approvalRecord, err := approvalRepo.Create(ctx, task.ID, currentVersion.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}
	if err := taskRepo.UpdateStatus(ctx, task.ID, models.StatusAwaitingApproval, nil); err != nil {
		t.Fatalf("mark task awaiting approval: %v", err)
	}

	versionIndexer := indexer.NewService(resourceRepo, flowEmbedder{})
	exec := executoragent.New(taskRepo, resourceRepo, versionIndexer)
	worker := job.New(jobRepo, exec, taskRepo, 1, taskevents.New(eventRepo), nil, nil)
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	worker.Start(workerCtx, 1)

	approvalService := approval.NewService(pool, approvalRepo, jobRepo, taskRepo, worker.JobCh(), taskevents.New(eventRepo), nil, nil)
	if _, err := approvalService.Approve(ctx, approvalRecord.ID); err != nil {
		t.Fatalf("approve task: %v", err)
	}
	waitForFlowTaskCompleted(t, ctx, taskRepo, task.ID)

	resourceHandler := NewResourceHandler(resourceRepo, nil)
	resourceDetailBody := getFlowResource(t, resourceHandler, resourceID)
	if !strings.Contains(resourceDetailBody, "最终修订内容") {
		t.Fatalf("expected resource detail to expose revised content, got %q", resourceDetailBody)
	}

	exportBody := exportFlowResource(t, resourceHandler, resourceID)
	if !strings.Contains(exportBody, "最终修订内容") {
		t.Fatalf("expected exported markdown to contain revised content, got %q", exportBody)
	}
}

type flowUploadResponse struct {
	Resource *struct {
		ID string `json:"id"`
	} `json:"resource"`
	Messages []struct {
		Kind    string          `json:"kind"`
		Payload json.RawMessage `json:"payload"`
	} `json:"messages"`
	FileID string
}

func uploadFlowFile(t *testing.T, handler *AssistantHandler, sessionID string, fileName string, content []byte) flowUploadResponse {
	t.Helper()

	engine := server.New()
	engine.POST("/api/assistant/sessions/:id/files", handler.UploadFile)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write multipart body: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	response := ut.PerformRequest(
		engine.Engine,
		"POST",
		"/api/assistant/sessions/"+sessionID+"/files",
		&ut.Body{Body: body, Len: body.Len()},
		ut.Header{Key: "Content-Type", Value: writer.FormDataContentType()},
	).Result()
	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected upload status %d, got %d body=%q", consts.StatusOK, response.StatusCode(), string(response.Body()))
	}

	var upload flowUploadResponse
	if err := json.Unmarshal(response.Body(), &upload); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	for _, message := range upload.Messages {
		if message.Kind != assistant.KindSessionFile {
			continue
		}

		var payload assistant.SessionFilePayload
		if err := json.Unmarshal(message.Payload, &payload); err != nil {
			t.Fatalf("decode session file payload: %v", err)
		}
		upload.FileID = payload.FileID
	}

	return upload
}

func downloadFlowFile(t *testing.T, repo *postgres.UploadedFileRepo, store *filestore.LocalStore, fileID string) string {
	t.Helper()

	handler := NewFileHandler(repo, store)
	engine := server.New()
	engine.GET("/api/files/:id/download", handler.Download)

	response := ut.PerformRequest(engine.Engine, "GET", "/api/files/"+fileID+"/download", nil).Result()
	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected file download status %d, got %d body=%q", consts.StatusOK, response.StatusCode(), string(response.Body()))
	}

	return string(response.Body())
}

func getFlowResource(t *testing.T, handler *ResourceHandler, resourceID string) string {
	t.Helper()

	engine := server.New()
	engine.GET("/api/resources/:id", handler.GetByID)

	response := ut.PerformRequest(engine.Engine, "GET", "/api/resources/"+resourceID, nil).Result()
	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected resource detail status %d, got %d body=%q", consts.StatusOK, response.StatusCode(), string(response.Body()))
	}

	return string(response.Body())
}

func exportFlowResource(t *testing.T, handler *ResourceHandler, resourceID string) string {
	t.Helper()

	engine := server.New()
	engine.GET("/api/resources/:id/export", handler.ExportCurrentVersion)

	response := ut.PerformRequest(engine.Engine, "GET", "/api/resources/"+resourceID+"/export", nil).Result()
	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected export status %d, got %d body=%q", consts.StatusOK, response.StatusCode(), string(response.Body()))
	}

	return string(response.Body())
}

func waitForFlowTaskCompleted(t *testing.T, ctx context.Context, taskRepo *postgres.TaskRepo, taskID string) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		currentTask, err := taskRepo.GetByID(ctx, taskID)
		if err != nil {
			t.Fatalf("get task: %v", err)
		}
		if currentTask == nil {
			t.Fatal("expected task to exist")
		}
		if currentTask.Status == models.StatusCompleted {
			return
		}
		if currentTask.Status == models.StatusFailed {
			t.Fatalf("expected task to complete, got failed with error %#v", currentTask.ErrorMessage)
		}

		time.Sleep(100 * time.Millisecond)
	}

	t.Fatal("timed out waiting for job to finish")
}

func cleanupFlowData(t *testing.T, pool *pgxpool.Pool, resourceID string, sessionID string, fileID string) {
	t.Helper()

	ctx := flowTestContext(t)
	if resourceID != "" {
		if err := postgrescleanup.CleanupResourceTree(ctx, pool, resourceID); err != nil {
			t.Fatalf("cleanup resource tree %q: %v", resourceID, err)
		}
	}
	if sessionID != "" {
		if _, err := pool.Exec(ctx, `DELETE FROM assistant_sessions WHERE id = $1`, sessionID); err != nil {
			t.Fatalf("cleanup assistant session %q: %v", sessionID, err)
		}
	}
	if fileID != "" {
		if _, err := pool.Exec(ctx, `DELETE FROM uploaded_files WHERE id = $1`, fileID); err != nil {
			t.Fatalf("cleanup uploaded file %q: %v", fileID, err)
		}
	}
}

func newFlowTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	cfg := appconfig.Load()
	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		t.Skip("database not available")
	}

	ctx := flowTestContext(t)
	return postgrestest.NewIsolatedPool(t, ctx, cfg.DatabaseURL, "server_files_flow", postgres.NewPool, postgres.RunMigrations)
}

func flowTestContext(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

type flowEmbedder struct{}

func (flowEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, 0, len(texts))
	for index := range texts {
		vector := make([]float32, 1024)
		vector[index%len(vector)] = 1
		vectors = append(vectors, vector)
	}

	return vectors, nil
}

func flowUniqueSuffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
