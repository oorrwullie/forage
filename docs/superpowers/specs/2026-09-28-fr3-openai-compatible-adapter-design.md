# FR-3: Scoped OpenAI-Compatible Adapter Design

## Purpose and scope

FR-3 adds Forage's first provider adapter. It translates the existing
provider-neutral `forage.Adapter.Chat(context.Context, Route, Request)` contract
to exactly one non-streaming OpenAI-compatible chat-completions HTTP request.
It does not select routes, discover models, retry, fall back, mutate policy, or
implement health/cooldown state.

The adapter will live in `provider/openai`, leaving the root package as the
provider-neutral policy and contract layer. `Route.Endpoint` is interpreted as
the exact chat-completions URL. This deliberately avoids URL rewriting and
provider-specific endpoint heuristics.

## Public boundary

`provider/openai.Adapter` will implement `forage.Adapter`. Construction will
accept an HTTP client and an optional bearer token. The adapter will add an
`Authorization: Bearer <token>` header only when a token is supplied. No
environment lookup, credential persistence, route-secret field, or discovery
mechanism is added.

The adapter accepts the current `Route` and `Request` unchanged. It rejects a
request with `Need.RequireJSONSchema` before issuing HTTP, returning
`*forage.AdapterError{Kind: forage.ErrorBadRequest}` because FR-3 has no
provider-neutral schema payload.

## Request and response flow

1. Build one `POST` bound to the supplied context and addressed exactly to
   `route.Endpoint`.
2. Encode JSON with `model: route.Model`, one `{role: "user", content:
   req.Input}` message, and `stream: false`.
3. Execute the request once through the configured HTTP client; no retry or
   route fallback is attempted.
4. Decode an ordinary non-streaming response with a provider-reported `model`,
   an assistant completion, and optional usage.
5. Return `forage.Response` with existing canonical `Execution` fields:
   route name, provider, requested model, effective provider-reported model,
   and normalized prompt/completion/reasoning token counts. Omitted usage stays
   zero-valued.

An absent reported model is preserved as empty. A non-empty reported model
different from `route.Model` returns `ErrorEffectiveModelMismatch` and no
successful response. A missing or malformed assistant completion is an
`ErrorProtocol`.

## Error policy

Every adapter-generated provider classification is returned as
`*forage.AdapterError` with the original diagnostic error retained when useful.

| Condition | Classification |
| --- | --- |
| 401 or 403 | `ErrorAuth` |
| 429 | `ErrorRateLimited`, including `Retry-After` when parseable |
| Explicitly identifiable request/context limit | `ErrorContextOverflow` |
| Other ordinary 4xx | `ErrorBadRequest` |
| 5xx | `ErrorUnavailable` |
| Malformed 2xx body or required completion structure | `ErrorProtocol` |
| Non-empty effective-model mismatch | `ErrorEffectiveModelMismatch` |

`Retry-After` accepts delta-seconds or HTTP-date values. Context construction
uses `http.NewRequestWithContext`; it does not add a retry that could mask a
caller cancellation/deadline.

## Testing and verification

`provider/openai` tests will use `httptest` and no real credentials or network.
They will establish: exact endpoint and wire JSON; one HTTP execution per call;
canonical success metadata and usage; effective-model mismatch; malformed 2xx;
401/403; 429 with retry-after; 5xx; ordinary invalid 4xx; context cancellation;
and JSON-schema rejection before dispatch.

Implementation will use test-first cycles. The final FR-3 verification is
`gofmt` on changed files, `go test -count=1 ./...`, `go vet ./...`,
`go test -race -count=1 -short ./...`, and `git diff --check`. No FR-4 work,
push, or unrelated refactor is part of this change.
