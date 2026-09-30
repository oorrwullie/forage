// Package facade exposes the authenticated local HTTP execution boundary.
package facade

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/oorrwullie/forage"
)

// MaxRequestBodyBytes bounds one facade request to one mebibyte.
const MaxRequestBodyBytes = 1 << 20

// Server holds immutable server-owned execution authority.
type Server struct {
	routes     []forage.Route
	dispatcher forage.Dispatcher
	token      []byte
}

// New constructs a facade with configured routes, the normal dispatcher, and
// a required local bearer token.
func New(routes []forage.Route, dispatcher forage.Dispatcher, bearerToken string) (*Server, error) {
	if bearerToken == "" {
		return nil, errors.New("facade bearer token is required")
	}
	return &Server{
		routes:     append([]forage.Route(nil), routes...),
		dispatcher: dispatcher,
		token:      []byte(bearerToken),
	}, nil
}

// Handler returns the local HTTP execution handler.
func (s *Server) Handler() http.Handler { return http.HandlerFunc(s.serveHTTP) }

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/chat" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.authorized(r.Header.Get("Authorization")) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !isJSON(r.Header.Get("Content-Type")) {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported media type")
		return
	}

	req, err := decodeRequest(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	decision := forage.Evaluate(s.routes, req.Need)
	if len(decision.Eligible) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "no eligible route")
		return
	}
	response, err := s.dispatcher.Dispatch(r.Context(), decision.Eligible, req)
	if err != nil {
		if errors.Is(err, forage.ErrNoEligibleRoute) {
			writeError(w, http.StatusUnprocessableEntity, "no eligible route")
			return
		}
		writeError(w, http.StatusBadGateway, "execution failed")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) authorized(header string) bool {
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || scheme != "Bearer" || token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), s.token) == 1
}

func isJSON(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && mediaType == "application/json"
}

type wireRequest struct {
	Input  string     `json:"input"`
	Need   wireNeed   `json:"need"`
	Budget wireBudget `json:"budget"`
}

type wireNeed struct {
	ZeroCost              bool     `json:"zero_cost"`
	Sensitivity           string   `json:"sensitivity"`
	AllowMayTrain         bool     `json:"allow_may_train"`
	AllowUnknownPolicy    bool     `json:"allow_unknown_policy"`
	RequireLocal          bool     `json:"require_local"`
	RequireChat           bool     `json:"require_chat"`
	RequireJSONSchema     bool     `json:"require_json_schema"`
	RequireUsage          bool     `json:"require_usage"`
	RequireReasoningUsage bool     `json:"require_reasoning_usage"`
	ContextTokens         int      `json:"context_tokens"`
	PinnedModel           string   `json:"pinned_model"`
	ExcludedModels        []string `json:"excluded_models"`
}

type wireBudget struct {
	MaxAttempts int `json:"max_attempts"`
}

func decodeRequest(w http.ResponseWriter, r *http.Request) (forage.Request, error) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxRequestBodyBytes))
	decoder.DisallowUnknownFields()
	var wire wireRequest
	if err := decoder.Decode(&wire); err != nil {
		return forage.Request{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return forage.Request{}, errors.New("request contains trailing JSON")
	}
	return forage.Request{
		Input: wire.Input,
		Need: forage.Need{
			ZeroCost:              wire.Need.ZeroCost,
			Sensitivity:           forage.Sensitivity(wire.Need.Sensitivity),
			AllowMayTrain:         wire.Need.AllowMayTrain,
			AllowUnknownPolicy:    wire.Need.AllowUnknownPolicy,
			RequireLocal:          wire.Need.RequireLocal,
			RequireChat:           wire.Need.RequireChat,
			RequireJSONSchema:     wire.Need.RequireJSONSchema,
			RequireUsage:          wire.Need.RequireUsage,
			RequireReasoningUsage: wire.Need.RequireReasoningUsage,
			ContextTokens:         wire.Need.ContextTokens,
			PinnedModel:           wire.Need.PinnedModel,
			ExcludedModels:        wire.Need.ExcludedModels,
		},
		Budget: forage.Budget{MaxAttempts: wire.Budget.MaxAttempts},
	}, nil
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, struct {
		Error string `json:"error"`
	}{Error: message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// ValidateListenAddress accepts only an explicit loopback host and numeric port.
func ValidateListenAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || port == "" {
		return fmt.Errorf("invalid listen address %q", address)
	}
	portNumber, err := strconv.ParseUint(port, 10, 16)
	if err != nil || portNumber > 65535 {
		return fmt.Errorf("invalid listen address %q", address)
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("listen address %q is not loopback", address)
	}
	return nil
}
