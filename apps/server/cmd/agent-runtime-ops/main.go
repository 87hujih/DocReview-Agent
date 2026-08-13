package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"agent_project/apps/server/internal/agent/operations"
	"agent_project/apps/server/internal/storage/postgres"
	"agent_project/apps/server/internal/storage/postgres/agentops"
)

type operationsService interface {
	Diagnose(context.Context, operations.DiagnosticRequest) (operations.Diagnostic, error)
	Metrics(context.Context, operations.MetricsRequest) (operations.MetricsSnapshot, error)
	Comparisons(context.Context, operations.ComparisonListRequest) (operations.ComparisonList, error)
	Cancel(context.Context, operations.ActionRequest) (operations.ActionResult, error)
	Retry(context.Context, operations.ActionRequest) (operations.ActionResult, error)
	ReplayDeadLetter(context.Context, operations.ActionRequest) (operations.ActionResult, error)
}

// main 初始化依赖并启动当前命令。
func main() {
	os.Exit(runMain(os.Args[1:], os.Stdout, os.Stderr))
}

// runMain 执行该函数负责的核心处理逻辑。
func runMain(args []string, stdout, stderr io.Writer) int {
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		fmt.Fprintln(stderr, "DATABASE_URL must be set explicitly; dotenv files are not loaded")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := postgres.NewPool(ctx, databaseURL)
	if err != nil {
		fmt.Fprintf(stderr, "connect operations database: %v\n", err)
		return 2
	}
	defer pool.Close()
	service, err := operations.NewService(agentops.NewRepository(pool))
	if err != nil {
		fmt.Fprintf(stderr, "initialize operations service: %v\n", err)
		return 2
	}
	return runCommand(ctx, args, service, time.Now().UTC(), stdout, stderr)
}

// runCommand 执行该函数负责的核心处理逻辑。
func runCommand(ctx context.Context, args []string, service operationsService, now time.Time, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("agent-runtime-ops", flag.ContinueOnError)
	flags.SetOutput(stderr)
	action := flags.String("action", "", "diagnose, metrics, comparisons, cancel, retry, or replay-dead-letter")
	workspaceID := flags.String("workspace-id", "", "trusted Workspace UUID")
	resourceID := flags.String("resource-id", "", "exact Resource UUID cohort; optional for metrics and required for comparisons")
	runID := flags.String("run-id", "", "durable Run UUID")
	eventID := flags.String("event-id", "", "Outbox event UUID")
	requestID := flags.String("request-id", "", "stable operator idempotency request ID")
	operatorID := flags.String("operator-id", "", "authenticated operator identity")
	reason := flags.String("reason", "", "incident or operational reason")
	window := flags.Duration("window", time.Hour, "read-only query window between 1m and 720h")
	limit := flags.Int("limit", 200, "comparison rows between 1 and 1000")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	*action = strings.TrimSpace(*action)
	*workspaceID = strings.TrimSpace(*workspaceID)
	*resourceID = strings.TrimSpace(*resourceID)
	*runID = strings.TrimSpace(*runID)
	*eventID = strings.TrimSpace(*eventID)
	*requestID = strings.TrimSpace(*requestID)
	*operatorID = strings.TrimSpace(*operatorID)
	*reason = strings.TrimSpace(*reason)
	if service == nil || *workspaceID == "" {
		fmt.Fprintln(stderr, "operations service and workspace-id are required")
		return 2
	}

	var output any
	var err error
	// 根据当前状态或类型选择对应的处理分支。
	switch *action {
	case "diagnose":
		if *runID == "" {
			fmt.Fprintln(stderr, "diagnose requires run-id")
			return 2
		}
		output, err = service.Diagnose(ctx, operations.DiagnosticRequest{WorkspaceID: *workspaceID, RunID: *runID})
	case "metrics":
		output, err = service.Metrics(ctx, operations.MetricsRequest{WorkspaceID: *workspaceID, ResourceID: *resourceID, Window: *window})
	case "comparisons":
		if *resourceID == "" {
			fmt.Fprintln(stderr, "comparisons requires resource-id")
			return 2
		}
		output, err = service.Comparisons(ctx, operations.ComparisonListRequest{
			WorkspaceID: *workspaceID, ResourceID: *resourceID, Window: *window, Limit: *limit,
		})
	case "cancel", "retry", "replay-dead-letter":
		if *requestID == "" || *operatorID == "" || *reason == "" || now.IsZero() {
			fmt.Fprintln(stderr, "mutating actions require request-id, operator-id, reason, and a valid clock")
			return 2
		}
		request := operations.ActionRequest{
			WorkspaceID: *workspaceID, RequestID: *requestID, OperatorID: *operatorID,
			Reason: *reason, RunID: *runID, EventID: *eventID, RequestedAt: now,
		}
		// 根据当前状态或类型选择对应的处理分支。
		switch *action {
		case "cancel":
			if *runID == "" || *eventID != "" {
				fmt.Fprintln(stderr, "cancel requires only run-id")
				return 2
			}
			output, err = service.Cancel(ctx, request)
		case "retry":
			if *runID == "" || *eventID != "" {
				fmt.Fprintln(stderr, "retry requires only run-id")
				return 2
			}
			output, err = service.Retry(ctx, request)
		case "replay-dead-letter":
			if *eventID == "" || *runID != "" {
				fmt.Fprintln(stderr, "replay-dead-letter requires only event-id")
				return 2
			}
			output, err = service.ReplayDeadLetter(ctx, request)
		}
	default:
		fmt.Fprintln(stderr, "action must be diagnose, metrics, comparisons, cancel, retry, or replay-dead-letter")
		return 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s failed: %v\n", *action, err)
		return 1
	}
	encoded, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "encode operation result: %v\n", err)
		return 2
	}
	if _, err := fmt.Fprintln(stdout, string(encoded)); err != nil {
		fmt.Fprintf(stderr, "write operation result: %v\n", err)
		return 2
	}
	return 0
}
