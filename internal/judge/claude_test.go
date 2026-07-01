package judge

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
)

// fakeResp is a canned HTTP response the hermetic Anthropic server returns.
type fakeResp struct {
	status      int
	contentType string // defaults to application/json
	body        string
}

// messageJSON renders a minimal but valid /v1/messages success body whose single
// text content block carries text (the structured-output verdict JSON in success cases).
func messageJSON(stopReason, text string) string {
	m := map[string]any{
		"id":            "msg_1",
		"type":          "message",
		"role":          "assistant",
		"model":         "claude-opus-4-8",
		"content":       []map[string]any{{"type": "text", "text": text}},
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage":         map[string]any{"input_tokens": 1, "output_tokens": 1},
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// messageJSONNoContent renders a success body with an empty content array.
func messageJSONNoContent(stopReason string) string {
	m := map[string]any{
		"id":            "msg_1",
		"type":          "message",
		"role":          "assistant",
		"model":         "claude-opus-4-8",
		"content":       []map[string]any{},
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage":         map[string]any{"input_tokens": 1, "output_tokens": 1},
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// errBody renders the {"error":{"type","message"}} envelope the SDK reads on >=400.
func errBody(typ, msg string) string {
	m := map[string]any{"type": "error", "error": map[string]any{"type": typ, "message": msg}}
	b, _ := json.Marshal(m)
	return string(b)
}

func newServer(t *testing.T, calls *int32, resp fakeResp) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		ct := resp.contentType
		if ct == "" {
			ct = "application/json"
		}
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(resp.status)
		_, _ = io.WriteString(w, resp.body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTestJudge(t *testing.T, url, model string, temp float64) *claudeJudge {
	t.Helper()
	client := anthropic.NewClient(
		option.WithBaseURL(url),
		option.WithAPIKey("test-key"),
		option.WithMaxRetries(0),
	)
	return newClaudeWithClient(client, model, temp)
}

func TestClaudeJudge(t *testing.T) {
	okBody := messageJSON("end_turn", `{"match":true,"reason":"the candidate restates the expected answer"}`)
	noMatchBody := messageJSON("end_turn", `{"match":false,"reason":"the candidate contradicts the expected answer"}`)

	tests := []struct {
		name        string
		apiKey      string // value set via t.Setenv before the call
		resp        fakeResp
		model       string
		wantErr     bool
		wantErrSub  string
		wantCalls   int32
		wantVerdict core.JudgeVerdict
	}{
		{
			name:       "missing api key errors before any call",
			apiKey:     "",
			resp:       fakeResp{status: 200, body: okBody},
			model:      "claude-opus-4-8",
			wantErr:    true,
			wantErrSub: "ANTHROPIC_API_KEY",
			wantCalls:  0,
		},
		{
			name:        "valid match verdict",
			apiKey:      "test-key",
			resp:        fakeResp{status: 200, body: okBody},
			model:       "claude-opus-4-8",
			wantCalls:   1,
			wantVerdict: core.JudgeVerdict{Match: true, Reason: "the candidate restates the expected answer"},
		},
		{
			name:        "valid no-match verdict",
			apiKey:      "test-key",
			resp:        fakeResp{status: 200, body: noMatchBody},
			model:       "claude-opus-4-8",
			wantCalls:   1,
			wantVerdict: core.JudgeVerdict{Match: false, Reason: "the candidate contradicts the expected answer"},
		},
		{
			name:       "malformed verdict json",
			apiKey:     "test-key",
			resp:       fakeResp{status: 200, body: messageJSON("end_turn", "this is not json")},
			model:      "claude-opus-4-8",
			wantErr:    true,
			wantErrSub: "verdict",
			wantCalls:  1,
		},
		{
			name:       "no text content block",
			apiKey:     "test-key",
			resp:       fakeResp{status: 200, body: messageJSONNoContent("end_turn")},
			model:      "claude-opus-4-8",
			wantErr:    true,
			wantErrSub: "no text content",
			wantCalls:  1,
		},
		{
			name:       "auth error 401",
			apiKey:     "test-key",
			resp:       fakeResp{status: 401, body: errBody("authentication_error", "invalid x-api-key")},
			model:      "claude-opus-4-8",
			wantErr:    true,
			wantErrSub: "authentication",
			wantCalls:  1,
		},
		{
			name:       "rate limit 429",
			apiKey:     "test-key",
			resp:       fakeResp{status: 429, body: errBody("rate_limit_error", "slow down")},
			model:      "claude-opus-4-8",
			wantErr:    true,
			wantErrSub: "rate limit",
			wantCalls:  1,
		},
		{
			name:       "server error 500",
			apiKey:     "test-key",
			resp:       fakeResp{status: 500, body: errBody("api_error", "boom")},
			model:      "claude-opus-4-8",
			wantErr:    true,
			wantErrSub: "backend",
			wantCalls:  1,
		},
		{
			name:       "other api error 400",
			apiKey:     "test-key",
			resp:       fakeResp{status: 400, body: errBody("invalid_request_error", "bad")},
			model:      "claude-opus-4-8",
			wantErr:    true,
			wantErrSub: "HTTP 400",
			wantCalls:  1,
		},
		{
			name:       "refusal stop reason",
			apiKey:     "test-key",
			resp:       fakeResp{status: 200, body: messageJSON("refusal", `{"match":true,"reason":"x"}`)},
			model:      "claude-opus-4-8",
			wantErr:    true,
			wantErrSub: "refus",
			wantCalls:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Serial by design: t.Setenv panics under t.Parallel().
			t.Setenv("ANTHROPIC_API_KEY", tt.apiKey)
			var calls int32
			srv := newServer(t, &calls, tt.resp)
			j := newTestJudge(t, srv.URL, tt.model, 0)

			got, err := j.Judge(context.Background(), core.JudgeRequest{Candidate: "c", Expected: "e"})

			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				if !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErrSub)
				}
			} else if got != tt.wantVerdict {
				t.Fatalf("verdict = %+v, want %+v", got, tt.wantVerdict)
			}
			if n := atomic.LoadInt32(&calls); n != tt.wantCalls {
				t.Fatalf("server calls = %d, want %d", n, tt.wantCalls)
			}
		})
	}
}

func TestClaudeJudge_TemperatureGating(t *testing.T) {
	tests := []struct {
		name       string
		model      string
		temp       float64
		wantErr    bool
		wantErrSub string
		wantTemp   bool  // whether the request body should carry a "temperature" field
		wantCalls  int32 // HTTP calls reaching the backend (0 when gated before the call)
	}{
		{name: "opus omits default temperature", model: "claude-opus-4-8", temp: 0.0, wantTemp: false, wantCalls: 1},
		{name: "fable omits default temperature", model: "claude-fable-5", temp: 0.0, wantTemp: false, wantCalls: 1},
		{name: "sonnet sends temperature", model: "claude-sonnet-4-6", temp: 0.5, wantTemp: true, wantCalls: 1},
		{name: "haiku sends temperature", model: "claude-haiku-4-5", temp: 0.5, wantTemp: true, wantCalls: 1},
		{
			name:       "opus with temperature errors before any call",
			model:      "claude-opus-4-8",
			temp:       0.5,
			wantErr:    true,
			wantErrSub: "does not accept a temperature parameter",
			wantCalls:  0,
		},
		{
			name:       "fable with temperature errors before any call",
			model:      "claude-fable-5",
			temp:       0.7,
			wantErr:    true,
			wantErrSub: "does not accept a temperature parameter",
			wantCalls:  0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ANTHROPIC_API_KEY", "test-key")
			var calls int32
			var reqBody []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&calls, 1)
				reqBody, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, messageJSON("end_turn", `{"match":true,"reason":"ok"}`))
			}))
			t.Cleanup(srv.Close)

			j := newTestJudge(t, srv.URL, tt.model, tt.temp)
			_, err := j.Judge(context.Background(), core.JudgeRequest{Candidate: "c", Expected: "e"})

			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr && !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErrSub)
			}
			if n := atomic.LoadInt32(&calls); n != tt.wantCalls {
				t.Fatalf("server calls = %d, want %d", n, tt.wantCalls)
			}
			hasTemp := bytes.Contains(reqBody, []byte(`"temperature"`))
			if hasTemp != tt.wantTemp {
				t.Fatalf("temperature present=%v want=%v; body=%s", hasTemp, tt.wantTemp, reqBody)
			}
		})
	}
}

func TestClaudeJudge_ContextCanceled(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	var calls int32
	srv := newServer(t, &calls, fakeResp{status: 200, body: messageJSON("end_turn", `{"match":true,"reason":"ok"}`)})
	j := newTestJudge(t, srv.URL, "claude-opus-4-8", 0)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := j.Judge(ctx, core.JudgeRequest{Candidate: "c", Expected: "e"})
	if err == nil {
		t.Fatal("expected error on canceled context, got nil")
	}
	if !strings.Contains(err.Error(), "context") {
		t.Fatalf("error %q does not name the context cause", err.Error())
	}
}

func TestClaudeJudge_TransportError(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	// Spin up then immediately close the server so the connection is refused.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	client := anthropic.NewClient(
		option.WithBaseURL(url),
		option.WithAPIKey("test-key"),
		option.WithMaxRetries(0),
	)
	j := newClaudeWithClient(client, "claude-opus-4-8", 0)

	_, err := j.Judge(context.Background(), core.JudgeRequest{Candidate: "c", Expected: "e"})
	if err == nil {
		t.Fatal("expected transport error, got nil")
	}
	if !strings.Contains(err.Error(), "Anthropic") {
		t.Fatalf("error %q does not name the calling cause", err.Error())
	}
}

func TestNewClaude(t *testing.T) {
	j, err := NewClaude(config.Config{Judge: config.JudgeConfig{Model: "claude-opus-4-8"}})
	if err != nil {
		t.Fatalf("NewClaude returned error: %v", err)
	}
	if j == nil {
		t.Fatal("NewClaude returned a nil Judge")
	}
}
