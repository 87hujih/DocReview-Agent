package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"agent_project/apps/server/internal/agent/cutover"
	"agent_project/apps/server/internal/agent/operations"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run reads only explicit JSON artifacts and never loads application config, dotenv, or a database.
func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("agent-shadow-review", flag.ContinueOnError)
	flags.SetOutput(stderr)
	action := flags.String("action", "", "template or verify")
	comparisonsPath := flags.String("comparisons", "", "path to agent-runtime-ops comparisons JSON")
	reviewPath := flags.String("review", "", "path to completed shadow review manifest")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	*action = strings.TrimSpace(*action)
	*comparisonsPath = strings.TrimSpace(*comparisonsPath)
	*reviewPath = strings.TrimSpace(*reviewPath)
	if flags.NArg() != 0 || *comparisonsPath == "" || (*action != "template" && *action != "verify") {
		fmt.Fprintln(stderr, "action template|verify and -comparisons are required")
		return 2
	}

	export, raw, err := readStrictJSON[operations.ComparisonList](*comparisonsPath)
	if err != nil {
		fmt.Fprintf(stderr, "read comparison export: %v\n", err)
		return 2
	}
	digest := artifactDigest(raw)
	var output any
	blocked := false
	switch *action {
	case "template":
		if *reviewPath != "" {
			fmt.Fprintln(stderr, "template does not accept -review")
			return 2
		}
		output, err = cutover.NewShadowReviewTemplate(export, digest)
	case "verify":
		if *reviewPath == "" {
			fmt.Fprintln(stderr, "verify requires -review")
			return 2
		}
		manifest, _, readErr := readStrictJSON[cutover.ShadowReviewManifest](*reviewPath)
		if readErr != nil {
			fmt.Fprintf(stderr, "read shadow review: %v\n", readErr)
			return 2
		}
		var report cutover.ShadowReviewReport
		report, err = cutover.EvaluateShadowReview(export, digest, manifest)
		output = report
		blocked = !report.EligibleForEvidence
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s shadow review: %v\n", *action, err)
		return 2
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(output); err != nil {
		fmt.Fprintf(stderr, "encode shadow review output: %v\n", err)
		return 2
	}
	if blocked {
		return 1
	}
	return 0
}

func readStrictJSON[T any](path string) (T, []byte, error) {
	var zero T
	raw, err := os.ReadFile(path)
	if err != nil {
		return zero, nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value T
	if err := decoder.Decode(&value); err != nil {
		return zero, nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return zero, nil, fmt.Errorf("trailing JSON value")
		}
		return zero, nil, err
	}
	return value, raw, nil
}

func artifactDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return fmt.Sprintf("sha256:%x", digest[:])
}
