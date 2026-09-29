package forage

import (
	"context"
	"errors"
)

// ErrNoEligibleRoute reports that no candidate survived final authorization.
var ErrNoEligibleRoute = errors.New("no eligible route")

// RouteResolver returns the current authoritative route by stable name.
type RouteResolver interface {
	Route(name string) (Route, bool)
}

// AdapterResolver returns the adapter configured for an explicitly declared provider.
type AdapterResolver interface {
	Adapter(provider string) (Adapter, bool)
}

// Dispatcher owns the only normal execution path to Adapter.Chat.
type Dispatcher struct {
	Routes   RouteResolver
	Adapters AdapterResolver
}

// Dispatch re-resolves and rechecks each candidate immediately before Chat.
// Candidate routes therefore never act as cached authorization.
func (d Dispatcher) Dispatch(ctx context.Context, candidates []Route, req Request) (Response, error) {
	if d.Routes == nil || d.Adapters == nil {
		return Response{}, ErrNoEligibleRoute
	}
	var attempts int
	var lastErr error
	for _, candidate := range candidates {
		current, ok := d.Routes.Route(candidate.Name)
		if !ok || len(Evaluate([]Route{current}, req.Need).Eligible) != 1 {
			continue
		}
		if req.Budget.MaxAttempts > 0 && attempts >= req.Budget.MaxAttempts {
			break
		}
		adapter, ok := d.Adapters.Adapter(current.Provider)
		if !ok || adapter == nil {
			return Response{}, ErrNoEligibleRoute
		}

		attempts++
		response, err := adapter.Chat(ctx, current, req)
		if err == nil {
			return response, nil
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return Response{}, err
		}
		var adapterErr *AdapterError
		if !errors.As(err, &adapterErr) || !adapterErr.FallbackEligible() {
			return Response{}, err
		}
		lastErr = err
	}
	if lastErr != nil {
		return Response{}, lastErr
	}
	return Response{}, ErrNoEligibleRoute
}
