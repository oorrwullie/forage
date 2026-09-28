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

// Dispatcher owns the only normal execution path to Adapter.Chat.
type Dispatcher struct {
	Routes  RouteResolver
	Adapter Adapter
}

// Dispatch re-resolves and rechecks each candidate immediately before Chat.
// Candidate routes therefore never act as cached authorization.
func (d Dispatcher) Dispatch(ctx context.Context, candidates []Route, req Request) (Response, error) {
	if d.Routes == nil || d.Adapter == nil {
		return Response{}, ErrNoEligibleRoute
	}
	for _, candidate := range candidates {
		current, ok := d.Routes.Route(candidate.Name)
		if !ok || len(Evaluate([]Route{current}, req.Need).Eligible) != 1 {
			continue
		}

		response, err := d.Adapter.Chat(ctx, current, req)
		if err == nil {
			return response, nil
		}
		return Response{}, err
	}
	return Response{}, ErrNoEligibleRoute
}
