package logging

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestNewLoggerWritesJSONWithBaseFields(t *testing.T) {
	t.Setenv("APP_ENV", "test")

	var output bytes.Buffer
	logger := NewLogger("server", "debug", "json", false, &output)
	logger.Info("hello observability")

	var payload map[string]any
	if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal log payload: %v", err)
	}

	if payload["service"] != "server" {
		t.Fatalf("expected service %q, got %#v", "server", payload["service"])
	}

	if payload["env"] != "test" {
		t.Fatalf("expected env %q, got %#v", "test", payload["env"])
	}

	if payload["msg"] != "hello observability" {
		t.Fatalf("expected msg %q, got %#v", "hello observability", payload["msg"])
	}
}
