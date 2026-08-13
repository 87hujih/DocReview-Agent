package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"agent_project/apps/server/internal/agent/cutover"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run 从显式证据文件生成删除报告；它不加载应用配置、dotenv 或数据库连接。
func run(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("agent-legacy-report", flag.ContinueOnError)
	flags.SetOutput(stderr)
	evidencePath := flags.String("evidence", "", "path to legacy-removal evidence JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*evidencePath) == "" {
		fmt.Fprintln(stderr, "-evidence is required")
		return 2
	}

	evidence, err := readEvidence(*evidencePath)
	if err != nil {
		fmt.Fprintf(stderr, "read removal evidence: %v\n", err)
		return 2
	}
	report, err := cutover.EvaluateLegacyRemoval(evidence)
	if err != nil {
		fmt.Fprintf(stderr, "evaluate removal evidence: %v\n", err)
		return 2
	}

	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(report); err != nil {
		fmt.Fprintf(stderr, "encode removal report: %v\n", err)
		return 2
	}
	if !report.Eligible {
		return 1
	}
	return 0
}

func readEvidence(path string) (cutover.LegacyRemovalEvidence, error) {
	file, err := os.Open(path)
	if err != nil {
		return cutover.LegacyRemovalEvidence{}, err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var evidence cutover.LegacyRemovalEvidence
	if err := decoder.Decode(&evidence); err != nil {
		return cutover.LegacyRemovalEvidence{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return cutover.LegacyRemovalEvidence{}, fmt.Errorf("trailing JSON value")
		}
		return cutover.LegacyRemovalEvidence{}, err
	}
	return evidence, nil
}
