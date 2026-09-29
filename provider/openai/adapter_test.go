package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oorrwullie/forage"
)

func TestChatSendsOneNonStreamingRequestAndReturnsCanonicalExecution(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/v1/chat/completions" || r.URL.RawQuery != "tenant=alpha&version=2" {
			t.Errorf("request target = %q, want exact path and query", r.URL.RequestURI())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret-token" {
			t.Errorf("Authorization = %q, want bearer token", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			Stream bool `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if body.Model != "model-x" || len(body.Messages) != 1 || body.Messages[0].Role != "user" || body.Messages[0].Content != "hello" || body.Stream {
			t.Errorf("request body = %#v, want model-x, one user message hello, stream false", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"model-x","choices":[{"message":{"role":"assistant","content":"hi there"}}],"usage":{"prompt_tokens":7,"completion_tokens":3,"completion_tokens_details":{"reasoning_tokens":2}}}`))
	}))
	defer server.Close()

	route := forage.Route{
		Name: "primary", Provider: "openai-compatible", Model: "model-x",
		Endpoint: server.URL + "/v1/chat/completions?tenant=alpha&version=2",
	}
	resp, err := New(server.Client(), "secret-token").Chat(context.Background(), route, forage.Request{Input: "hello"})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want exactly one", requests)
	}
	if resp.Output != "hi there" {
		t.Errorf("Output = %q, want %q", resp.Output, "hi there")
	}
	want := forage.Execution{
		Route: "primary", Provider: "openai-compatible", RequestedModel: "model-x", EffectiveModel: "model-x",
		Usage: forage.Usage{InputTokens: 7, OutputTokens: 3, ReasoningTokens: 2},
	}
	if resp.Execution != want {
		t.Errorf("Execution = %#v, want %#v", resp.Execution, want)
	}
}

func TestChatRejectsEffectiveModelMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"model":"other-model","choices":[{"message":{"role":"assistant","content":"unexpected"}}]}`))
	}))
	defer server.Close()

	resp, err := New(server.Client(), "").Chat(context.Background(), testRoute(server.URL), forage.Request{})
	assertAdapterError(t, resp, err, forage.ErrorEffectiveModelMismatch)
}

func TestChatDoesNotFollowRedirects(t *testing.T) {
	for _, status := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var destinationRequests atomic.Int32
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				destinationRequests.Add(1)
				_, _ = w.Write([]byte(`{"model":"model-x","choices":[{"message":{"role":"assistant","content":"redirected"}}]}`))
			}))
			defer destination.Close()
			var endpointRequests atomic.Int32
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				endpointRequests.Add(1)
				http.Redirect(w, r, destination.URL, status)
			}))
			defer endpoint.Close()
			client := endpoint.Client()
			resp, err := New(client, "").Chat(context.Background(), testRoute(endpoint.URL), forage.Request{Input: "private input"})
			if err == nil || resp != (forage.Response{}) {
				t.Errorf("Chat() = %#v, %v, want empty response and error", resp, err)
			}
			if got := destinationRequests.Load(); got != 0 {
				t.Errorf("redirect destination requests = %d, want zero", got)
			}
			if got := endpointRequests.Load(); got != 1 {
				t.Errorf("endpoint requests = %d, want exactly one", got)
			}
			if client.CheckRedirect != nil {
				t.Error("adapter mutated the injected client's redirect policy")
			}
		})
	}
}

func TestChatRejectsJSONSchemaBeforeHTTP(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	resp, err := New(server.Client(), "").Chat(context.Background(), testRoute(server.URL), forage.Request{
		Need: forage.Need{RequireJSONSchema: true},
	})
	assertAdapterError(t, resp, err, forage.ErrorBadRequest)
	if requests != 0 {
		t.Fatalf("requests = %d, want zero", requests)
	}
}

func TestChatPreservesCallerContextCancellation(t *testing.T) {
	for _, tc := range []struct {
		name string
		want error
		ctx  func() (context.Context, context.CancelFunc)
	}{
		{name: "canceled", want: context.Canceled, ctx: func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) }},
		{name: "deadline exceeded", want: context.DeadlineExceeded, ctx: func() (context.Context, context.CancelFunc) {
			return context.WithDeadline(context.Background(), time.Now().Add(150*time.Millisecond))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observed := make(chan struct{})
			releaseHandler := make(chan struct{})
			var requests int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				_, _ = io.Copy(io.Discard, r.Body)
				_ = r.Body.Close()
				close(observed)
				select {
				case <-r.Context().Done():
				case <-releaseHandler:
				}
			}))
			defer func() {
				close(releaseHandler)
				server.Close()
			}()

			ctx, cancel := tc.ctx()
			defer cancel()
			result := make(chan error, 1)
			go func() {
				_, err := New(server.Client(), "").Chat(ctx, testRoute(server.URL), forage.Request{})
				result <- err
			}()
			select {
			case <-observed:
			case <-time.After(time.Second):
				t.Fatal("server did not observe request")
			}
			if tc.name == "canceled" {
				cancel()
			}
			select {
			case err := <-result:
				if !errors.Is(err, tc.want) {
					t.Fatalf("Chat() error = %v, want errors.Is(_, %v)", err, tc.want)
				}
			case <-time.After(time.Second):
				t.Fatal("Chat did not return after context termination")
			}
			if requests != 1 {
				t.Errorf("requests = %d, want exactly one", requests)
			}
		})
	}
}

type failingRoundTripper struct{ err error }

func (rt failingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) { return nil, rt.err }

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func TestChatRejectsTerminatedContextBeforeHTTP(t *testing.T) {
	for _, tc := range []struct {
		name string
		want error
		ctx  func() (context.Context, context.CancelFunc)
	}{
		{name: "canceled", want: context.Canceled, ctx: func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx, cancel
		}},
		{name: "deadline exceeded", want: context.DeadlineExceeded, ctx: func() (context.Context, context.CancelFunc) {
			return context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls int
			client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return nil, errors.New("unexpected transport invocation")
			})}
			ctx, cancel := tc.ctx()
			defer cancel()
			resp, err := New(client, "").Chat(ctx, testRoute("http://example.invalid"), forage.Request{})
			if !errors.Is(err, tc.want) || resp != (forage.Response{}) {
				t.Errorf("Chat() = %#v, %v, want empty response and %v", resp, err, tc.want)
			}
			if calls != 0 {
				t.Errorf("RoundTrip calls = %d, want zero", calls)
			}
			_, err = New(client, "").Chat(ctx, testRoute(":invalid URL"), forage.Request{Need: forage.Need{RequireJSONSchema: true}})
			if !errors.Is(err, tc.want) {
				t.Errorf("Chat() with invalid request error = %v, want %v before construction/validation", err, tc.want)
			}
		})
	}
}

type bodyReadObserver struct {
	io.ReadCloser
	started chan struct{}
}

func (body bodyReadObserver) Read(p []byte) (int, error) {
	select {
	case body.started <- struct{}{}:
	default:
	}
	return body.ReadCloser.Read(p)
}

func TestChatPreservesContextDuringErrorBodyRead(t *testing.T) {
	for _, tc := range []struct {
		name string
		want error
		ctx  func() (context.Context, context.CancelFunc)
	}{
		{name: "canceled", want: context.Canceled, ctx: func() (context.Context, context.CancelFunc) {
			return context.WithCancel(context.Background())
		}},
		{name: "deadline exceeded", want: context.DeadlineExceeded, ctx: func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), 500*time.Millisecond)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte(`{"error":`))
				w.(http.Flusher).Flush()
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			defer func() {
				close(release)
				server.Close()
			}()
			started := make(chan struct{}, 1)
			client := server.Client()
			transport := client.Transport
			client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				resp, err := transport.RoundTrip(req)
				if err == nil {
					resp.Body = bodyReadObserver{ReadCloser: resp.Body, started: started}
				}
				return resp, err
			})
			ctx, cancel := tc.ctx()
			defer cancel()
			result := make(chan error, 1)
			go func() {
				resp, err := New(client, "").Chat(ctx, testRoute(server.URL), forage.Request{})
				if resp != (forage.Response{}) {
					t.Errorf("response = %#v, want empty response", resp)
				}
				result <- err
			}()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("adapter did not start reading the received HTTP error body")
			}
			if tc.want == context.Canceled {
				cancel()
			}
			select {
			case err := <-result:
				if !errors.Is(err, tc.want) {
					t.Errorf("Chat() error = %v, want errors.Is(_, %v)", err, tc.want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Chat did not return after context termination during error body read")
			}
		})
	}
}

func TestChatClassifiesPreResponseTransportFailure(t *testing.T) {
	wantCause := errors.New("dial failed")
	client := &http.Client{Transport: failingRoundTripper{err: wantCause}}
	resp, err := New(client, "").Chat(context.Background(), testRoute("http://example.invalid"), forage.Request{})
	adapterErr := assertAdapterError(t, resp, err, forage.ErrorUnavailable)
	if !errors.Is(err, wantCause) {
		t.Errorf("Chat() error = %v, want to retain cause %v", err, wantCause)
	}
	if adapterErr.Err == nil {
		t.Fatal("AdapterError.Err is nil, want transport cause")
	}
}

func TestChatRejectsMalformedSuccessfulResponse(t *testing.T) {
	for name, body := range map[string]string{
		"malformed JSON":  `{"choices":`,
		"missing choices": `{"model":"model-x","choices":[]}`,
		"missing message": `{"model":"model-x","choices":[{}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			resp, err := New(server.Client(), "").Chat(context.Background(), testRoute(server.URL), forage.Request{})
			assertAdapterError(t, resp, err, forage.ErrorProtocol)
		})
	}
}

func TestChatRequiresAssistantContent(t *testing.T) {
	for _, tc := range []struct {
		name  string
		body  string
		valid bool
	}{
		{name: "missing content", body: `{"choices":[{"message":{"role":"assistant"}}]}`},
		{name: "null content", body: `{"choices":[{"message":{"role":"assistant","content":null}}]}`},
		{name: "explicit empty content", body: `{"choices":[{"message":{"role":"assistant","content":""}}]}`, valid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			resp, err := New(server.Client(), "").Chat(context.Background(), testRoute(server.URL), forage.Request{})
			if !tc.valid {
				assertAdapterError(t, resp, err, forage.ErrorProtocol)
				return
			}
			if err != nil || resp.Output != "" || resp.Execution.Route != "primary" {
				t.Errorf("Chat() = %#v, %v, want successful empty completion", resp, err)
			}
		})
	}
}

func TestChatRequiresSingleJSONResponse(t *testing.T) {
	for _, tc := range []struct {
		name   string
		suffix string
		valid  bool
	}{
		{name: "second JSON object", suffix: ` {"extra":true}`},
		{name: "second JSON scalar", suffix: ` null`},
		{name: "trailing garbage", suffix: ` garbage`},
		{name: "trailing whitespace", suffix: " \t\r\n", valid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hello"}}]}` + tc.suffix))
			}))
			defer server.Close()
			resp, err := New(server.Client(), "").Chat(context.Background(), testRoute(server.URL), forage.Request{})
			if !tc.valid {
				assertAdapterError(t, resp, err, forage.ErrorProtocol)
				return
			}
			if err != nil || resp.Output != "hello" {
				t.Errorf("Chat() = %#v, %v, want successful completion", resp, err)
			}
		})
	}
}

func TestChatClassifiesHTTPFailures(t *testing.T) {
	future := time.Now().UTC().Add(90 * time.Second).Truncate(time.Second)
	cases := []struct {
		name       string
		status     int
		body       string
		retryAfter string
		kind       forage.ErrorKind
		retryCheck func(time.Duration) bool
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, kind: forage.ErrorAuth},
		{name: "forbidden", status: http.StatusForbidden, kind: forage.ErrorAuth},
		{name: "rate limit seconds", status: http.StatusTooManyRequests, retryAfter: "7", kind: forage.ErrorRateLimited, retryCheck: func(d time.Duration) bool { return d == 7*time.Second }},
		{name: "rate limit HTTP date", status: http.StatusTooManyRequests, retryAfter: future.Format(http.TimeFormat), kind: forage.ErrorRateLimited, retryCheck: func(d time.Duration) bool { return d > 0 && d <= 90*time.Second }},
		{name: "server error", status: http.StatusBadGateway, kind: forage.ErrorUnavailable},
		{name: "ordinary client error", status: http.StatusBadRequest, body: `{"error":{"message":"invalid parameter"}}`, kind: forage.ErrorBadRequest},
		{name: "context window parameter error", status: http.StatusBadRequest, body: `{"error":{"message":"context window must be an integer"}}`, kind: forage.ErrorBadRequest},
		{name: "context size error", status: http.StatusBadRequest, body: `{"error":{"code":"context_length_exceeded","message":"maximum context length exceeded"}}`, kind: forage.ErrorContextOverflow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.retryAfter != "" {
					w.Header().Set("Retry-After", tc.retryAfter)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			resp, err := New(server.Client(), "").Chat(context.Background(), testRoute(server.URL), forage.Request{})
			adapterErr := assertAdapterError(t, resp, err, tc.kind)
			if tc.retryCheck != nil && !tc.retryCheck(adapterErr.RetryAfter) {
				t.Errorf("RetryAfter = %s, unexpected for %q", adapterErr.RetryAfter, tc.retryAfter)
			}
		})
	}
}

func testRoute(endpoint string) forage.Route {
	return forage.Route{Name: "primary", Provider: "openai-compatible", Model: "model-x", Endpoint: endpoint}
}

func assertAdapterError(t *testing.T, resp forage.Response, err error, kind forage.ErrorKind) *forage.AdapterError {
	t.Helper()
	if resp != (forage.Response{}) {
		t.Errorf("response = %#v, want empty response", resp)
	}
	var adapterErr *forage.AdapterError
	if err == nil || !errors.As(err, &adapterErr) {
		t.Fatalf("error = %v, want *forage.AdapterError", err)
	}
	if adapterErr.Kind != kind {
		t.Errorf("error kind = %q, want %q", adapterErr.Kind, kind)
	}
	return adapterErr
}
