package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"agent_project/apps/server/internal/agent/evaluation"
)

// main 初始化依赖并启动当前命令。
func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// 运行执行该函数负责的核心处理逻辑。
func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("agent-eval", flag.ContinueOnError)
	flags.SetOutput(stderr)
	datasetPath := flags.String("dataset", "", "path to the versioned evaluation dataset")
	candidatePath := flags.String("candidate", "", "path to recorded deterministic candidate outcomes")
	reportPath := flags.String("report", "-", "report path, or - for stdout")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*datasetPath) == "" || strings.TrimSpace(*candidatePath) == "" || strings.TrimSpace(*reportPath) == "" {
		fmt.Fprintln(stderr, "dataset, candidate, and report are required")
		return 2
	}
	datasetJSON, err := os.ReadFile(*datasetPath)
	if err != nil {
		fmt.Fprintf(stderr, "read dataset: %v\n", err)
		return 2
	}
	candidateJSON, err := os.ReadFile(*candidatePath)
	if err != nil {
		fmt.Fprintf(stderr, "read candidate: %v\n", err)
		return 2
	}
	report, err := evaluation.EvaluateJSON(datasetJSON, candidateJSON)
	if err != nil {
		fmt.Fprintf(stderr, "evaluate candidate: %v\n", err)
		return 2
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "encode report: %v\n", err)
		return 2
	}
	encoded = append(encoded, '\n')
	if *reportPath == "-" {
		if _, err := stdout.Write(encoded); err != nil {
			fmt.Fprintf(stderr, "write report: %v\n", err)
			return 2
		}
	} else if err := os.WriteFile(*reportPath, encoded, 0o644); err != nil {
		fmt.Fprintf(stderr, "write report: %v\n", err)
		return 2
	}
	if report.Status != evaluation.StatusPassed {
		return 1
	}
	return 0
}
