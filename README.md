<p align="center">
  <img src="docs/assets/forage-banner.png" alt="Forage — Discover · Gather · Build" width="100%">
</p>

# Forage

> **Forage finds the right model for the job.**
>
> Provider-independent, cost-aware inference routing constrained by capability,
> privacy, and budget.

Forage is a Go inference router for choosing and executing an explicitly
configured model route under caller-declared constraints.

A caller describes what an inference request needs — for example, chat support,
local execution, a minimum context window, repository-sensitive handling, or
zero-cost inference. Forage evaluates configured routes, rejects routes that do
not satisfy the request, and dispatches through an eligible provider adapter.

Forage deliberately answers a narrow question:

> **Which currently configured execution route is allowed to handle this
> request?**

## Why Forage?

Choosing a model becomes more interesting once "use model X" is no longer
enough.

The right route may depend on:

- whether inference must cost $0;
- whether data may leave the local machine;
- the route's declared data policy;
- required model capabilities;
- required context capacity;
- a caller-pinned model;
- and whether the route is still authorized immediately before execution.

Forage makes those constraints explicit.

    caller
       |
       | Need + Budget + Input
       v
    +--------+
    | Forage |
    +---+----+
        |
        | policy evaluation
        v
    eligible routes
    (advisory only)
        |
        v
    dispatch gate
    re-resolve + recheck
        |
        v
    provider adapter
       / \
      /   \
    Ollama  OpenAI-compatible

An eligible candidate is not cached authorization. Immediately before provider
execution, the dispatcher re-resolves the current route and re-evaluates it
against the original request.

## Current capabilities

Forage currently provides:

- explicitly configured YAML routes;
- provider-independent policy evaluation;
- caller-declared data sensitivity;
- zero-cost routing constraints;
- local-only routing constraints;
- capability matching for chat, JSON Schema, usage reporting,
  reasoning-token usage, and context capacity;
- optional model pinning;
- bounded dispatch attempts;
- fallback across independently eligible routes;
- dispatch-time route revalidation;
- native Ollama chat execution;
- OpenAI-compatible chat-completions execution;
- provider-neutral execution metadata;
- requested-model/effective-model verification;
- an authenticated loopback-only HTTP facade.

Responses are currently non-streaming.

## Requirements

- Go 1.25 or newer
- at least one configured inference route
- the corresponding provider endpoint, such as Ollama or an
  OpenAI-compatible service

## Quick start

Clone and test:

    git clone https://github.com/oorrwullie/forage.git
    cd forage
    go test ./...

Create a local configuration:

    cp config.example.yaml forage.local.yaml
    go run ./cmd/forage forage.local.yaml

A successful configuration validation exits without output.

The example configuration contains a local Qwen route through Ollama:

    routes:
      - name: local-qwen
        provider: ollama
        model: qwen3-coder:30b
        endpoint: http://127.0.0.1:11434/api/chat
        cost: free
        data_policy: local
        capabilities:
          chat: true
          json_schema: false
          usage: true
          reasoning_usage: false
          context_tokens: 262144

## Run the local facade

Forage exposes an authenticated inference boundary at:

    POST /v1/chat

The facade requires a bearer token and accepts only an explicit loopback listen
address.

Start it:

    export FORAGE_FACADE_TOKEN='replace-with-a-local-secret'

    go run ./cmd/forage serve \
      --config forage.local.yaml \
      --listen 127.0.0.1:8787

Example request:

    curl \
      --fail-with-body \
      --request POST \
      --header "Authorization: Bearer ${FORAGE_FACADE_TOKEN}" \
      --header 'Content-Type: application/json' \
      --data '{
        "input": "Explain why deterministic systems are useful.",
        "need": {
          "zero_cost": true,
          "sensitivity": "public",
          "require_local": true,
          "require_chat": true
        },
        "budget": {
          "max_attempts": 2
        }
      }' \
      http://127.0.0.1:8787/v1/chat

Successful responses include provider-neutral execution evidence: the selected
route, provider, requested model, effective model, and provider-reported usage.

## Request constraints

A request describes a Need, a Budget, and the inference input.

Need can constrain routing by:

| Constraint | Purpose |
| --- | --- |
| ZeroCost | Require a route explicitly declared free |
| Sensitivity | Declare the sensitivity of submitted data |
| AllowMayTrain | Explicitly permit an applicable may-train route |
| AllowUnknownPolicy | Explicitly permit an applicable unknown data policy |
| RequireLocal | Require local execution |
| RequireChat | Require chat capability |
| RequireJSONSchema | Require JSON Schema capability |
| RequireUsage | Require usage reporting |
| RequireReasoningUsage | Require reasoning-token reporting |
| ContextTokens | Require at least this context capacity |
| PinnedModel | Require an exact configured model |
| ExcludedModels | Reject routes configured with any exact excluded model identity |

Budget.MaxAttempts bounds provider execution attempts for one dispatch.

`ExcludedModels` is a caller-declared negative route constraint. A route is
ineligible when its configured model exactly matches an excluded identity. If
the same model is configured on multiple routes, every such route is
ineligible.

This guarantees that Forage will not intentionally dispatch a route configured
with an excluded model. It does not create a normalized cross-provider model
identity: comparisons use the exact configured model string. Providers may
currently omit effective-model identity; when they report a non-empty effective
model, the existing effective-model mismatch check remains authoritative.

## Sensitivity and data policy

Forage does **not** inspect a prompt and guess whether it is sensitive.

The caller explicitly declares one of:

    public
    repository
    sensitive

Unknown sensitivity fails policy evaluation.

Configured routes declare one of:

    local
    no-train
    may-train
    unknown

Local routes are permitted for recognized sensitivity classes.

Repository or sensitive data requires an appropriate route policy or explicit
caller permission where policy allows that permission.

The caller owns classification. Forage enforces the declared constraint.

## Dispatch safety

Initial policy evaluation produces an **advisory** ordered candidate list.

Immediately before provider execution, the dispatcher:

1. resolves the route again by stable name;
2. evaluates the current route against the original request;
3. enforces the attempt budget;
4. resolves the adapter for the current provider;
5. only then calls Adapter.Chat.

A route that was eligible earlier therefore cannot act as cached authorization.

Supported provider-independent failures may permit fallback to another
independently eligible route.

## Effective-model verification

Execution evidence records both the requested and effective model.

If a provider reports a non-empty model identity different from the configured
model, Forage reports an effective-model mismatch rather than silently
accepting the response.

The model that actually performed inference is therefore part of the execution
evidence.

## Providers

### Ollama

The Ollama adapter uses Ollama's native non-streaming chat API.

A route points directly at the native chat endpoint, for example:

    http://127.0.0.1:11434/api/chat

### OpenAI-compatible

The OpenAI-compatible adapter uses a non-streaming chat-completions endpoint.

If authentication is required, provide the bearer token at runtime:

    export FORAGE_OPENAI_BEARER_TOKEN='...'

Provider credentials are runtime authority and do not belong in persisted route
configuration.

## Configuration

Forage intentionally uses explicit configuration rather than provider
discovery.

Routes declare their identity, provider, model, endpoint, cost, data policy,
and capabilities.

Supported cost classes:

    free
    paid
    unknown

Supported data policies:

    local
    no-train
    may-train
    unknown

Unknown YAML fields are rejected, route names must be unique, and required
route identity fields must be present.

See [config.example.yaml](config.example.yaml).

## HTTP boundary

The facade intentionally has a small surface:

    POST /v1/chat
    Authorization: Bearer <FORAGE_FACADE_TOKEN>
    Content-Type: application/json

It:

- requires authentication;
- compares bearer tokens in constant time;
- rejects unknown request fields;
- rejects trailing JSON values;
- bounds request-body size;
- rejects requests with no eligible route;
- accepts only explicit loopback listen addresses.

Provider endpoint, cost, data policy, and other execution authority remain
server-owned configuration.

## Design boundaries

Forage is intentionally narrow.

It is not intended to be:

- an agent framework;
- a workflow engine;
- a job scheduler;
- a prompt-sensitivity classifier;
- a provider/model discovery service;
- a benchmark suite;
- a transparent replacement for every provider API.

Those concerns belong above or beside Forage.

**Forage owns route policy and the inference execution boundary.**

## Status

Forage is early-stage software and its public API may evolve.

The current implementation has been exercised as a real inference boundary:
Foreman has used Forage to route a development inference request to a local
Qwen model through Ollama under a zero-cost policy.

That boundary is deliberate: Foreman owns workflow and process lifecycle;
Forage owns inference-route authorization and dispatch.

## Development

Run the deterministic local checks:

    go test ./...
    go vet ./...

Contributions should preserve the central trust-boundary invariant:

> **A candidate route is advisory. Authorization is re-established immediately
> before provider execution.**

See [CONTRIBUTING.md](CONTRIBUTING.md).

## Security

Please do not report security vulnerabilities through a public GitHub issue.

See [SECURITY.md](SECURITY.md).

## License

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE).
