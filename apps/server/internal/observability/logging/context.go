package logging

import "context"

type contextKey string

const (
	requestIDKey contextKey = "request_id"
	taskIDKey    contextKey = "task_id"
	runIDKey     contextKey = "run_id"
	stepNameKey  contextKey = "step_name"
)

// WithRequestID 把 request_id 写入上下文。
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey, requestID)
}

// RequestID 从上下文中读取 request_id。
func RequestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}

// WithTaskID 把 task_id 写入上下文。
func WithTaskID(ctx context.Context, taskID string) context.Context {
	return context.WithValue(ctx, taskIDKey, taskID)
}

// TaskID 从上下文中读取 task_id。
func TaskID(ctx context.Context) string {
	value, _ := ctx.Value(taskIDKey).(string)
	return value
}

// WithRunID 把 run_id 写入上下文。
func WithRunID(ctx context.Context, runID string) context.Context {
	return context.WithValue(ctx, runIDKey, runID)
}

// RunID 从上下文中读取 run_id。
func RunID(ctx context.Context) string {
	value, _ := ctx.Value(runIDKey).(string)
	return value
}

// WithStepName 把 step_name 写入上下文。
func WithStepName(ctx context.Context, stepName string) context.Context {
	return context.WithValue(ctx, stepNameKey, stepName)
}

// StepName 从上下文中读取 step_name。
func StepName(ctx context.Context) string {
	value, _ := ctx.Value(stepNameKey).(string)
	return value
}
