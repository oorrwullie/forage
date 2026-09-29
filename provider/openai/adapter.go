package openai

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

// Adapter sends non-streaming chat completion requests to an OpenAI-compatible endpoint.
type Adapter struct {
	client      *http.Client
	bearerToken string
}

// New constructs an adapter with an injected HTTP client and optional bearer token.
func New(client *http.Client, bearerToken string) *Adapter {
	ownedClient := *client
	ownedClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Adapter{client: &ownedClient, bearerToken: bearerToken}
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Role    string  `json:"role"`
			Content *string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens      int `json:"prompt_tokens"`
		CompletionTokens  int `json:"completion_tokens"`
		CompletionDetails struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	} `json:"usage"`
}

// Chat sends one non-streaming user message to the route's exact endpoint.
func (a *Adapter) Chat(ctx context.Context, route forage.Route, req forage.Request) (forage.Response, error) {
	if err := ctx.Err(); err != nil {
		return forage.Response{}, err
	}
	if req.Need.RequireJSONSchema {
		return forage.Response{}, &forage.AdapterError{Kind: forage.ErrorBadRequest, Err: fmt.Errorf("JSON schema output is not supported")}
	}
	body, err := json.Marshal(chatRequest{
		Model:    route.Model,
		Messages: []chatMessage{{Role: "user", Content: req.Input}},
		Stream:   false,
	})
	if err != nil {
		return forage.Response{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, route.Endpoint, bytes.NewReader(body))
	if err != nil {
		return forage.Response{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if a.bearerToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+a.bearerToken)
	}

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
	var completion chatResponse
	decoder := json.NewDecoder(httpResp.Body)
	if err := decoder.Decode(&completion); err != nil {
		return forage.Response{}, &forage.AdapterError{Kind: forage.ErrorProtocol, Err: fmt.Errorf("decode chat completion: %w", err)}
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("additional JSON value")
		}
		return forage.Response{}, &forage.AdapterError{Kind: forage.ErrorProtocol, Err: fmt.Errorf("trailing chat completion data: %w", err)}
	}
	if len(completion.Choices) == 0 || completion.Choices[0].Message.Role != "assistant" || completion.Choices[0].Message.Content == nil {
		return forage.Response{}, &forage.AdapterError{Kind: forage.ErrorProtocol, Err: fmt.Errorf("chat completion has no assistant choice/message")}
	}
	if completion.Model != "" && completion.Model != route.Model {
		return forage.Response{}, &forage.AdapterError{Kind: forage.ErrorEffectiveModelMismatch, Err: fmt.Errorf("requested model %q, received model %q", route.Model, completion.Model)}
	}

	return forage.Response{
		Output: *completion.Choices[0].Message.Content,
		Execution: forage.Execution{
			Route:          route.Name,
			Provider:       route.Provider,
			RequestedModel: route.Model,
			EffectiveModel: completion.Model,
			Usage: forage.Usage{
				InputTokens:     completion.Usage.PromptTokens,
				OutputTokens:    completion.Usage.CompletionTokens,
				ReasoningTokens: completion.Usage.CompletionDetails.ReasoningTokens,
			},
		},
	}, nil
}

func classifyHTTPFailure(ctx context.Context, resp *http.Response) error {
	body, readErr := io.ReadAll(resp.Body)
	statusErr := fmt.Errorf("chat completion returned HTTP status %d", resp.StatusCode)
	if readErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
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
