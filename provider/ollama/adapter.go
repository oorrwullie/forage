// Package ollama adapts the native Ollama chat API to Forage's Adapter contract.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/oorrwullie/forage"
)

// Adapter sends non-streaming native Ollama chat requests.
type Adapter struct{ client *http.Client }

// New constructs an adapter with an injected HTTP client.
func New(client *http.Client) *Adapter {
	ownedClient := *client
	ownedClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Adapter{client: &ownedClient}
}

type chatRequest struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
	Stream   bool      `json:"stream"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Message struct {
		Role    string  `json:"role"`
		Content *string `json:"content"`
	} `json:"message"`
	Done            bool `json:"done"`
	PromptEvalCount int  `json:"prompt_eval_count"`
	EvalCount       int  `json:"eval_count"`
}

// Chat sends one non-streaming user message to Route.Endpoint exactly.
func (a *Adapter) Chat(ctx context.Context, route forage.Route, req forage.Request) (forage.Response, error) {
	if err := ctx.Err(); err != nil {
		return forage.Response{}, err
	}
	body, err := json.Marshal(chatRequest{Model: route.Model, Messages: []message{{Role: "user", Content: req.Input}}, Stream: false})
	if err != nil {
		return forage.Response{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, route.Endpoint, bytes.NewReader(body))
	if err != nil {
		return forage.Response{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpResp, err := a.client.Do(httpReq)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return forage.Response{}, ctxErr
		}
		return forage.Response{}, &forage.AdapterError{Kind: forage.ErrorUnavailable, Err: err}
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode < http.StatusOK || httpResp.StatusCode >= http.StatusMultipleChoices {
		return forage.Response{}, classifyHTTPFailure(ctx, httpResp)
	}
	decoder := json.NewDecoder(httpResp.Body)
	var completion chatResponse
	if err := decoder.Decode(&completion); err != nil {
		return forage.Response{}, protocolError("decode native chat completion", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("additional JSON value")
		}
		return forage.Response{}, protocolError("trailing native chat completion data", err)
	}
	if !completion.Done || completion.Message.Role != "assistant" || completion.Message.Content == nil {
		return forage.Response{}, protocolError("native chat completion has no completed assistant message", nil)
	}
	if completion.Model != "" && completion.Model != route.Model {
		return forage.Response{}, &forage.AdapterError{Kind: forage.ErrorEffectiveModelMismatch, Err: fmt.Errorf("requested model %q, received model %q", route.Model, completion.Model)}
	}
	return forage.Response{Output: *completion.Message.Content, Execution: forage.Execution{
		Route: route.Name, Provider: route.Provider, RequestedModel: route.Model, EffectiveModel: completion.Model,
		Usage: forage.Usage{InputTokens: completion.PromptEvalCount, OutputTokens: completion.EvalCount},
	}}, nil
}

func protocolError(message string, err error) error {
	if err != nil {
		err = fmt.Errorf("%s: %w", message, err)
	} else {
		err = fmt.Errorf("%s", message)
	}
	return &forage.AdapterError{Kind: forage.ErrorProtocol, Err: err}
}

func classifyHTTPFailure(ctx context.Context, resp *http.Response) error {
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
	}
	statusErr := fmt.Errorf("native chat returned HTTP status %d", resp.StatusCode)
	if readErr != nil {
		statusErr = fmt.Errorf("%w (read error response: %v)", statusErr, readErr)
	}
	if detail := strings.TrimSpace(string(body)); detail != "" {
		statusErr = fmt.Errorf("%w: %s", statusErr, detail)
	}
	kind := forage.ErrorBadRequest
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		kind = forage.ErrorAuth
	case resp.StatusCode == http.StatusTooManyRequests:
		return &forage.AdapterError{Kind: forage.ErrorRateLimited, RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()), Err: statusErr}
	case resp.StatusCode >= http.StatusInternalServerError:
		kind = forage.ErrorUnavailable
	case resp.StatusCode == http.StatusRequestEntityTooLarge || isContextSizeFailure(body):
		kind = forage.ErrorContextOverflow
	}
	return &forage.AdapterError{Kind: kind, Err: statusErr}
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil && when.After(now) {
		return when.Sub(now)
	}
	return 0
}

func isContextSizeFailure(body []byte) bool {
	text := strings.ToLower(string(body))
	for _, marker := range []string{"context_length_exceeded", "maximum context length", "context length exceeded", "context window exceeded", "context limit exceeded", "request too large", "payload too large", "request size limit"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}
