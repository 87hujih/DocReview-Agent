package events

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"agent_project/apps/server/internal/storage/postgres"

	"github.com/jackc/pgx/v5"
)

var (
	// ErrTaskIDRequired 表示写入任务事件时缺少 task_id。
	ErrTaskIDRequired = errors.New("task_id 不能为空")
	// ErrSourceRequired 表示写入任务事件时缺少 source。
	ErrSourceRequired = errors.New("source 不能为空")
	// ErrEventTypeRequired 表示写入任务事件时缺少 event_type。
	ErrEventTypeRequired = errors.New("event_type 不能为空")
	// ErrMessageRequired 表示写入任务事件时缺少 message。
	ErrMessageRequired = errors.New("message 不能为空")
)

type taskEventWriter interface {
	Add(ctx context.Context, input postgres.TaskEventCreateParams) (*postgres.TaskEvent, error)
	AddTx(ctx context.Context, tx pgx.Tx, input postgres.TaskEventCreateParams) (*postgres.TaskEvent, error)
}

// RecordInput 描述一次任务事件记录请求。
type RecordInput struct {
	TaskID    string
	RunID     *string
	StepName  string
	Source    string
	Level     string
	EventType string
	Message   string
	Payload   any
}

// Service 统一负责任务事件的校验和写入。
type Service struct {
	repo taskEventWriter
}

// New 创建任务事件服务。
func New(repo taskEventWriter) *Service {
	return &Service{repo: repo}
}

// Record 校验并写入一条任务事件。
func (s *Service) Record(ctx context.Context, input RecordInput) (*postgres.TaskEvent, error) {
	params, err := buildCreateParams(input)
	if err != nil {
		return nil, err
	}

	return s.repo.Add(ctx, params)
}

// RecordTx 校验并在事务内写入一条任务事件。
func (s *Service) RecordTx(ctx context.Context, tx pgx.Tx, input RecordInput) (*postgres.TaskEvent, error) {
	params, err := buildCreateParams(input)
	if err != nil {
		return nil, err
	}

	return s.repo.AddTx(ctx, tx, params)
}

func marshalPayload(payload any) ([]byte, error) {
	if payload == nil {
		return []byte(`{}`), nil
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	if len(encoded) == 0 {
		return []byte(`{}`), nil
	}

	return encoded, nil
}

func buildCreateParams(input RecordInput) (postgres.TaskEventCreateParams, error) {
	taskID := strings.TrimSpace(input.TaskID)
	if taskID == "" {
		return postgres.TaskEventCreateParams{}, ErrTaskIDRequired
	}

	source := strings.TrimSpace(input.Source)
	if source == "" {
		return postgres.TaskEventCreateParams{}, ErrSourceRequired
	}

	eventType := strings.TrimSpace(input.EventType)
	if eventType == "" {
		return postgres.TaskEventCreateParams{}, ErrEventTypeRequired
	}

	message := strings.TrimSpace(input.Message)
	if message == "" {
		return postgres.TaskEventCreateParams{}, ErrMessageRequired
	}

	level := strings.ToLower(strings.TrimSpace(input.Level))
	if level == "" {
		level = "info"
	}

	payload, err := marshalPayload(input.Payload)
	if err != nil {
		return postgres.TaskEventCreateParams{}, err
	}

	return postgres.TaskEventCreateParams{
		TaskID:    taskID,
		RunID:     input.RunID,
		StepName:  strings.TrimSpace(input.StepName),
		Source:    source,
		Level:     level,
		EventType: eventType,
		Message:   message,
		Payload:   payload,
	}, nil
}
