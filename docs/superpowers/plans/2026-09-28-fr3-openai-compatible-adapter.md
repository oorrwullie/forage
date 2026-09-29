# FR-3 OpenAI-Compatible Adapter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add one minimal, non-streaming OpenAI-compatible HTTP adapter that implements Forage's existing provider-neutral `Adapter` contract.

**Architecture:** Create `provider/openai` as the sole owner of OpenAI-compatible HTTP and JSON. Its adapter takes an injected `*http.Client` and optional bearer token, treats `Route.Endpoint` as the exact chat-completions URL, and returns existing `forage.Response` / `forage.Execution` values without modifying the root policy or dispatcher.

**Tech Stack:** Go 1.25, `net/http`, `encoding/json`, `httptest`, existing `forage` package.

**Spec:** `docs/superpowers/specs/2026-09-28-fr3-openai-compatible-adapter-design.md`

## Global Constraints

- `Route.Endpoint` is the exact chat-completions URL: no URL rewriting, discovery, or substitution.
- Inject the HTTP client; do not add hidden retry behavior. One `Adapter.Chat` call performs at most one HTTP request.
- Bearer tokens are adapter construction/runtime input only; never add them to `Route` or persisted configuration.
- Send one non-streaming request with `route.Model` and a single user message from `Request.Input`.
- Reject `Need.RequireJSONSchema` before HTTP with `*forage.AdapterError{Kind: forage.ErrorBadRequest}`.
- Preserve existing `forage.Response.Execution`; do not introduce an OpenAI-specific execution record.
- A non-empty provider model different from `Route.Model` is `ErrorEffectiveModelMismatch`; malformed required 2xx completion structure is `ErrorProtocol`.
- Preserve `context.Canceled` and `context.DeadlineExceeded` from a failed HTTP execution. Other pre-response transport failures are `*forage.AdapterError{Kind: forage.ErrorUnavailable}` retaining their cause.
- Do not alter policy, dispatcher behavior, fallback, cooldown, the root error taxonomy, or begin FR-4.
- Do not commit or push.

## Review Focus

- Exact endpoint with a path/query must be used unchanged; test this in Task 1.
- An already-canceled context must not create a request; test this in Task 3.
- A cancellation after server receipt must surface `context.Canceled`, not `ErrorUnavailable`; test this in Task 3.
- A non-context transport error before any response must retain its cause inside `ErrorUnavailable`; test this in Task 3.
- Both delta-seconds and HTTP-date `Retry-After` values must become non-zero rate-limit delays; test this in Task 2.
- A 2xx response with no assistant choice/message must fail closed as `ErrorProtocol`; test this in Task 2.

---

## File structure

- Create `provider/openai/adapter.go`: injected-client adapter, private wire DTOs, single request execution, response decoding, execution metadata, and status/error mapping.
- Create `provider/openai/adapter_test.go`: `httptest` contract tests and a custom round-tripper for pre-response transport failures.
- No root-package, route, policy, dispatcher, configuration, or dependency changes.

### Task 1: Successful exact-endpoint adapter request

**Files:**
- Create: `provider/openai/adapter.go`
- Test: `provider/openai/adapter_test.go`

**Interfaces:**
- Consumes: `forage.Adapter`, `forage.Route`, `forage.Request`, `forage.Response`.
- Produces: `func New(client *http.Client, bearerToken string) *Adapter` and `func (a *Adapter) Chat(ctx context.Context, route forage.Route, req forage.Request) (forage.Response, error)`.

- [ ] **Step 1: Write the failing success-contract test**

```go
func TestChatSendsOneNonStreamingRequestAndReturnsCanonicalExecution(t *testing.T)
```

Use an `httptest.Server` endpoint with a query string. Assert one request, exact path/query, `POST`, optional bearer header, `model == route.Model`, one `user` message containing `req.Input`, and `stream == false`. Return a completion with matching model, assistant content, prompt/completion/reasoning usage; assert the existing `forage.Execution` fields are populated and normalized.

- [ ] **Step 2: Run the focused test to verify it fails**

Run: `go test -count=1 ./provider/openai -run '^TestChatSendsOneNonStreamingRequestAndReturnsCanonicalExecution$'`

Expected: FAIL because package/adapter does not exist.

- [ ] **Step 3: Implement the minimal adapter success path**

Implement `New` and `(*Adapter).Chat` in `provider/openai/adapter.go`. Create the request with `http.NewRequestWithContext`, address it exactly to `route.Endpoint`, set JSON content type and bearer header only when non-empty, issue one `client.Do`, close the response body, and decode private request/response DTOs. On successful matching completion, return only the existing `forage.Response` and its canonical `Execution`.

- [ ] **Step 4: Run the focused test to verify it passes**

Run: `go test -count=1 ./provider/openai -run '^TestChatSendsOneNonStreamingRequestAndReturnsCanonicalExecution$'`

Expected: PASS.

### Task 2: Successful-response validation and HTTP status translation

**Files:**
- Modify: `provider/openai/adapter.go`
- Test: `provider/openai/adapter_test.go`

**Interfaces:**
- Consumes: `(*Adapter).Chat` from Task 1 and existing `*forage.AdapterError` taxonomy.
- Produces: classified 2xx validation and status failures without adding error kinds.

- [ ] **Step 1: Write failing classified-response tests**

```go
func TestChatRejectsEffectiveModelMismatch(t *testing.T)
func TestChatRejectsMalformedSuccessfulResponse(t *testing.T)
func TestChatClassifiesHTTPFailures(t *testing.T)
```

Assert a different non-empty response model returns `*forage.AdapterError` with `ErrorEffectiveModelMismatch` and an empty response; malformed JSON or missing assistant choice/message returns `ErrorProtocol`; 401/403 map to `ErrorAuth`, 429 to `ErrorRateLimited` with both delta-seconds and future HTTP-date `Retry-After` values parsed to positive delays, 5xx to `ErrorUnavailable`, ordinary 4xx to `ErrorBadRequest`, and an explicitly identified context/request-size failure to `ErrorContextOverflow`.

- [ ] **Step 2: Run the focused tests to verify they fail**

Run: `go test -count=1 ./provider/openai -run '^(TestChatRejectsEffectiveModelMismatch|TestChatRejectsMalformedSuccessfulResponse|TestChatClassifiesHTTPFailures)$'`

Expected: FAIL because classification/validation is incomplete.

- [ ] **Step 3: Implement minimal validation and status mapping**

Add private helpers in `provider/openai/adapter.go` to validate the 2xx completion shape, compare only non-empty effective model values, classify documented status ranges, and parse `Retry-After` as either delta-seconds or an HTTP date using `http.ParseTime` and the current time. Use `ErrorContextOverflow` only when the status/body explicitly identifies a context or request-size limit; otherwise preserve ordinary 4xx as `ErrorBadRequest`. Preserve useful diagnostic causes in `AdapterError.Err`.

- [ ] **Step 4: Run the focused tests to verify they pass**

Run: `go test -count=1 ./provider/openai -run '^(TestChatRejectsEffectiveModelMismatch|TestChatRejectsMalformedSuccessfulResponse|TestChatClassifiesHTTPFailures)$'`

Expected: PASS.

### Task 3: Pre-dispatch schema boundary and transport/cancellation semantics

**Files:**
- Modify: `provider/openai/adapter.go`
- Test: `provider/openai/adapter_test.go`

**Interfaces:**
- Consumes: `(*Adapter).Chat` and `Request.Need.RequireJSONSchema`.
- Produces: pre-dispatch schema rejection and transport failure behavior that preserves context semantics.

- [ ] **Step 1: Write the failing boundary tests**

```go
func TestChatRejectsJSONSchemaBeforeHTTP(t *testing.T)
func TestChatPreservesCallerContextCancellation(t *testing.T)
func TestChatClassifiesPreResponseTransportFailure(t *testing.T)
```

Count server requests to prove JSON-schema rejection makes zero HTTP calls. Use a server that observes the request then waits for cancellation, and assert `errors.Is(err, context.Canceled)` (repeat with a deadline context for `context.DeadlineExceeded`). Use an injected custom `RoundTripper` that returns a sentinel error and assert `errors.As` to `*forage.AdapterError`, `Kind == ErrorUnavailable`, and `errors.Is` finds the sentinel cause.

- [ ] **Step 2: Run the focused tests to verify they fail**

Run: `go test -count=1 ./provider/openai -run '^(TestChatRejectsJSONSchemaBeforeHTTP|TestChatPreservesCallerContextCancellation|TestChatClassifiesPreResponseTransportFailure)$'`

Expected: FAIL because the schema guard and transport distinction are incomplete.

- [ ] **Step 3: Implement the bounded pre-dispatch and transport handling**

Before request construction/execution, reject `RequireJSONSchema` as `ErrorBadRequest`. When the one `client.Do` call fails, return `ctx.Err()` unchanged when non-nil; otherwise return `*forage.AdapterError{Kind: forage.ErrorUnavailable, Err: cause}`. Do not add retry, alternate routes, or new error kinds.

- [ ] **Step 4: Run the focused tests to verify they pass**

Run: `go test -count=1 ./provider/openai -run '^(TestChatRejectsJSONSchemaBeforeHTTP|TestChatPreservesCallerContextCancellation|TestChatClassifiesPreResponseTransportFailure)$'`

Expected: PASS.

### Task 4: Full FR-3 verification and scope check

**Files:**
- Verify: `provider/openai/adapter.go`
- Verify: `provider/openai/adapter_test.go`

**Interfaces:**
- Consumes: complete Tasks 1–3 implementation.
- Produces: evidence that the scoped adapter is formatted, race-clean, and contains no FR-4 behavior.

- [ ] **Step 1: Format changed Go files**

Run: `gofmt -w provider/openai/adapter.go provider/openai/adapter_test.go`

Expected: files are formatted; inspect the resulting diff for only the adapter and its tests.

- [ ] **Step 2: Run deterministic repository verification**

Run: `go test -count=1 ./...`

Expected: PASS.

- [ ] **Step 3: Run static and race verification**

Run: `go vet ./... && go test -race -count=1 -short ./...`

Expected: both commands PASS.

- [ ] **Step 4: Check the candidate surface**

Run: `git diff --check && git status --short`

Expected: no whitespace errors; only the approved FR-3 adapter/test files and the already-approved design/plan documents are changed. No commit or push.

## Self-review

- Spec coverage: Tasks 1–3 cover the exact endpoint, injected client, optional runtime bearer token, one-request invariant, wire translation, canonical metadata, optional usage, model mismatch, protocol validation, status taxonomy, retry-after, schema boundary, and cancellation/transport distinction. Task 4 covers the requested verification and scope check.
- Type consistency: every task uses `*Adapter`, `New(*http.Client, string)`, and the unchanged `forage.Adapter.Chat` signature.
- Scope: no task edits root policy/dispatcher/configuration, adds dependencies, retries, route fallback, health state, or FR-4 work.
- Review-focus coverage: each listed condition is assigned to Tasks 1–3, including both standard `Retry-After` forms in Task 2.
