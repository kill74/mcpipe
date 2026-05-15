package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"mcpipe/internal/config"
)

func TestSendWebhookSuccess(t *testing.T) {
	var received Payload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("expected application/json, got %s", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("X-Custom") != "test-value" {
			t.Fatalf("missing custom header")
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := config.Notify{
		Channel:           "webhook",
		URL:               server.URL,
		Headers:           map[string]string{"X-Custom": "test-value"},
		IncludeRunID:      true,
		IncludeFailedStep: true,
	}
	err := Send(context.Background(), cfg, "pipeline.json", "run_1", "step_a", "timeout", time.Now(), time.Now(), 3, 5, 100, 200)
	if err != nil {
		t.Fatal(err)
	}
	if received.Event != "pipeline_failure" {
		t.Fatalf("unexpected event: %s", received.Event)
	}
	if received.RunID != "run_1" {
		t.Fatalf("unexpected run_id: %s", received.RunID)
	}
	if received.FailedStep != "step_a" {
		t.Fatalf("unexpected failed_step: %s", received.FailedStep)
	}
	if received.Error != "timeout" {
		t.Fatalf("unexpected error: %s", received.Error)
	}
	if received.Attempts != 3 {
		t.Fatalf("unexpected attempts: %d", received.Attempts)
	}
}

func TestSendNoURL(t *testing.T) {
	cfg := config.Notify{Channel: "stderr"}
	err := Send(context.Background(), cfg, "pipeline.json", "run_1", "", "error", time.Now(), time.Now(), 1, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
}

func TestSendRetryOnFailure(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := config.Notify{URL: server.URL}
	err := Send(context.Background(), cfg, "pipeline.json", "run_1", "", "error", time.Now(), time.Now(), 1, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
}

func TestSendAllRetriesFail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := config.Notify{URL: server.URL}
	err := Send(context.Background(), cfg, "pipeline.json", "run_1", "", "error", time.Now(), time.Now(), 1, 0, 0, 0)
	if err == nil {
		t.Fatal("expected error after all retries")
	}
}
