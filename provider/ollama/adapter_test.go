package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oorrwullie/forage"
)

func TestChatSendsNativeRequestAndNormalizesResponse(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/api/chat" || r.URL.RawQuery != "tenant=local" {
			t.Errorf("target = %q, want exact configured endpoint", r.URL.RequestURI())
		}
		var request struct {
			Model    string                           `json:"model"`
			Messages []struct{ Role, Content string } `json:"messages"`
			Stream   bool                             `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Model != "qwen3-coder:30b" || len(request.Messages) != 1 || request.Messages[0].Role != "user" || request.Messages[0].Content != "hello" || request.Stream {
			t.Errorf("native request = %#v", request)
		}
		_, _ = w.Write([]byte(`{"model":"qwen3-coder:30b","message":{"role":"assistant","content":"world"},"done":true,"done_reason":"stop","prompt_eval_count":15,"eval_count":4}`))
	}))
	defer server.Close()

	got, err := New(server.Client()).Chat(context.Background(), testRoute(server.URL+"/api/chat?tenant=local"), forage.Request{Input: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
	if got.Output != "world" || got.Execution != (forage.Execution{Route: "ollama", Provider: "ollama", RequestedModel: "qwen3-coder:30b", EffectiveModel: "qwen3-coder:30b", Usage: forage.Usage{InputTokens: 15, OutputTokens: 4}}) {
		t.Fatalf("response = %#v", got)
	}
}

func TestChatRejectsInvalidNativeSuccess(t *testing.T) {
	for _, body := range []string{
		`{`,
		`{"model":"qwen3-coder:30b","message":{"role":"assistant"},"done":true}`,
		`{"model":"qwen3-coder:30b","message":{"role":"assistant","content":null},"done":true}`,
		`{"model":"qwen3-coder:30b","message":{"role":"user","content":"x"},"done":true}`,
		`{"model":"qwen3-coder:30b","message":{"role":"assistant","content":"x"},"done":false}`,
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			assertKind(t, New(server.Client()), context.Background(), testRoute(server.URL), forage.ErrorProtocol)
		})
	}
}

func TestChatRejectsEffectiveModelMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"model":"other","message":{"role":"assistant","content":"x"},"done":true}`))
	}))
	defer server.Close()
	assertKind(t, New(server.Client()), context.Background(), testRoute(server.URL), forage.ErrorEffectiveModelMismatch)
}

func TestChatDoesNotFollowRedirects(t *testing.T) {
	var destination atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		destination.Add(1)
		_, _ = w.Write([]byte(`{"model":"qwen3-coder:30b","message":{"role":"assistant","content":"x"},"done":true}`))
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	got, err := New(origin.Client()).Chat(context.Background(), testRoute(origin.URL), forage.Request{})
	if err == nil || got != (forage.Response{}) || destination.Load() != 0 {
		t.Fatalf("response=%#v err=%v destination=%d", got, err, destination.Load())
	}
}

func TestChatPreservesContextAndTransportErrors(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) { calls.Add(1); return nil, errors.New("unexpected") })}
	got, err := New(client).Chat(canceled, testRoute("http://example.invalid"), forage.Request{})
	if got != (forage.Response{}) || !errors.Is(err, context.Canceled) || calls.Load() != 0 {
		t.Fatalf("canceled = %#v, %v, calls=%d", got, err, calls.Load())
	}

	deadline, deadlineCancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer deadlineCancel()
	got, err = New(client).Chat(deadline, testRoute("http://example.invalid"), forage.Request{})
	if got != (forage.Response{}) || !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 0 {
		t.Fatalf("deadline = %#v, %v, calls=%d", got, err, calls.Load())
	}

	cause := errors.New("dial failed")
	got, err = New(&http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, cause })}).Chat(context.Background(), testRoute("http://example.invalid"), forage.Request{})
	var adapterErr *forage.AdapterError
	if got != (forage.Response{}) || !errors.As(err, &adapterErr) || adapterErr.Kind != forage.ErrorUnavailable || !errors.Is(err, cause) {
		t.Fatalf("transport = %#v, %v", got, err)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testRoute(endpoint string) forage.Route {
	return forage.Route{Name: "ollama", Provider: "ollama", Model: "qwen3-coder:30b", Endpoint: endpoint}
}

func assertKind(t *testing.T, adapter *Adapter, ctx context.Context, route forage.Route, want forage.ErrorKind) {
	t.Helper()
	got, err := adapter.Chat(ctx, route, forage.Request{})
	var adapterErr *forage.AdapterError
	if got != (forage.Response{}) || !errors.As(err, &adapterErr) || adapterErr.Kind != want {
		t.Fatalf("response=%#v err=%v want=%s", got, err, want)
	}
}
