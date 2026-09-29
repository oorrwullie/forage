# Contributing to Forage

Thanks for your interest in Forage.

Forage is intentionally small. Contributions are most useful when they make
route policy, execution safety, provider support, or operability measurably
better without turning Forage into a general-purpose orchestration framework.

## Development

Before submitting a change, format changed Go files and run:

    go test ./...
    go vet ./...

## Design principles

Please preserve these invariants:

- callers explicitly declare request sensitivity;
- Forage does not infer sensitivity from prompt content;
- route configuration is explicit;
- policy fails closed when required authority is unknown;
- eligible candidates are advisory, not authorization;
- the dispatcher re-resolves and revalidates a route immediately before
  provider execution;
- provider adapters do not own policy authority;
- execution evidence records requested and effective model identity;
- secrets are runtime authority and do not belong in route configuration.

A change that intentionally alters one of these invariants should explain why.

## Scope

Forage is an inference router, not an agent framework or workflow engine.

Before adding a large abstraction, consider whether it belongs in Forage or in
a caller layered above it.

## Tests

Behavior changes should include tests.

Prefer deterministic tests using local test servers and injected dependencies.
Tests should not require paid inference, external credentials, or live provider
availability.

## Pull requests

Keep pull requests focused and describe:

- the problem being solved;
- the behavioral or architectural change;
- relevant trust-boundary implications;
- how the change was verified.
