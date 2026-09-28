package forage

import (
	"context"
	"fmt"
	"time"
)

// Budget bounds one request across future dispatch attempts.
type Budget struct {
	Deadline    time.Time
	MaxAttempts int
}

// Request carries a caller-declared Need and transport-neutral input.
type Request struct {
	Need   Need
	Budget Budget
	Input  string
}

// Usage is provider-neutral token evidence.
type Usage struct {
	InputTokens     int
	OutputTokens    int
	ReasoningTokens int
}

// Execution is the single canonical provider-neutral execution/evidence record.
type Execution struct {
	Route          string
	Provider       string
	RequestedModel string
	EffectiveModel string
	Usage          Usage
}

// Response is a plain non-streaming completion result.
type Response struct {
	Output    string
	Execution Execution
}

// Adapter executes one route. It receives a route and request, never policy.
type Adapter interface {
	Chat(context.Context, Route, Request) (Response, error)
}

// ErrorKind classifies provider-independent adapter failure behavior.
type ErrorKind string

const (
	ErrorRateLimited            ErrorKind = "rate_limited"
	ErrorQuotaExhausted         ErrorKind = "quota_exhausted"
	ErrorUnavailable            ErrorKind = "unavailable"
	ErrorBadRequest             ErrorKind = "bad_request"
	ErrorAuth                   ErrorKind = "auth"
	ErrorContextOverflow        ErrorKind = "context_overflow"
	ErrorProtocol               ErrorKind = "protocol"
	ErrorEffectiveModelMismatch ErrorKind = "effective_model_mismatch"
)

// AdapterError is a typed adapter failure. RetryAfter is meaningful for rate limits.
type AdapterError struct {
	Kind       ErrorKind
	RetryAfter time.Duration
	Err        error
}

func (e *AdapterError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("adapter %s: %v", e.Kind, e.Err)
	}
	return fmt.Sprintf("adapter %s", e.Kind)
}

func (e *AdapterError) Unwrap() error { return e.Err }

// FallbackEligible reports whether an independently eligible route may be tried.
func (e *AdapterError) FallbackEligible() bool {
	switch e.Kind {
	case ErrorRateLimited, ErrorQuotaExhausted, ErrorUnavailable, ErrorContextOverflow, ErrorProtocol, ErrorEffectiveModelMismatch:
		return true
	default:
		return false
	}
}

// RouteDisabling identifies failures that later health/cooldown mechanics must treat as unsafe.
func (e *AdapterError) RouteDisabling() bool {
	return e.Kind == ErrorAuth || e.Kind == ErrorEffectiveModelMismatch
}
