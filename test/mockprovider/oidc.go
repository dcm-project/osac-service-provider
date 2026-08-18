package mockprovider

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// mockAccessToken is the static bearer token every successful grant
// receives. The mock's own gRPC server never validates it (there is
// nothing downstream of the mock to protect) — it only needs to satisfy
// osac-sp's client, not a resource server, so no real JWT signing is
// needed (spec §1).
const mockAccessToken = "mock-access-token"

// mockTokenExpiresInSeconds is the expires_in every token response
// reports.
const mockTokenExpiresInSeconds = 3600

// discoveryDocument is the subset of RFC 8414 / OpenID Connect Discovery
// 1.0 metadata internal/osac/bootstrap.go's discoverTokenEndpoint reads.
type discoveryDocument struct {
	TokenEndpoint string `json:"token_endpoint"`
}

// tokenResponse is an RFC 6749 §5.1 access token response.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// tokenErrorResponse is an RFC 6749 §5.2 error response.
type tokenErrorResponse struct {
	Error string `json:"error"`
}

// OIDCHandler serves the fake OIDC discovery-and-token endpoints
// internal/osac.Bootstrap's discoveringTokenSource needs (REQ-MOCK-080/
// 090/100): both well-known discovery documents (in the same order
// bootstrap.go tries them) and a client-credentials token endpoint.
//
// Client credentials (via HTTP Basic auth or form body) are never
// validated — the mock's gRPC server doesn't enforce auth either, so
// there is nothing to check them against. This means both credential
// -delivery styles REQ-MOCK-090 requires acceptance of are inherently
// accepted, by simply never being inspected.
type OIDCHandler struct {
	mux    *http.ServeMux
	logger *slog.Logger
}

// NewOIDCHandler returns an OIDCHandler whose discovery documents
// advertise tokenURL as the token_endpoint. tokenURL must be the handler's
// own eventual "<base>/token" address — callers typically only know this
// after opening the real net.Listener they'll serve this handler on (see
// cmd/osac-mock-provider/main.go). logger records the rare case of a
// response failing to encode (headers are already sent by then, so there
// is nothing more to do than log it — same pattern as
// internal/httperror.Write).
func NewOIDCHandler(tokenURL string, logger *slog.Logger) *OIDCHandler {
	h := &OIDCHandler{mux: http.NewServeMux(), logger: logger}

	doc := discoveryDocument{TokenEndpoint: tokenURL}
	serveDoc := func(w http.ResponseWriter, _ *http.Request) {
		h.writeJSON(w, http.StatusOK, doc)
	}
	h.mux.HandleFunc("/.well-known/oauth-authorization-server", serveDoc)
	h.mux.HandleFunc("/.well-known/openid-configuration", serveDoc)
	h.mux.HandleFunc("/token", h.handleToken)

	return h
}

// ServeHTTP implements http.Handler.
func (h *OIDCHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *OIDCHandler) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.writeTokenError(w, "invalid_request")
		return
	}

	switch grantType := r.FormValue("grant_type"); grantType {
	case "":
		h.writeTokenError(w, "invalid_request")
	case "client_credentials":
		h.writeJSON(w, http.StatusOK, tokenResponse{
			AccessToken: mockAccessToken,
			TokenType:   "Bearer",
			ExpiresIn:   mockTokenExpiresInSeconds,
		})
	default:
		h.writeTokenError(w, "unsupported_grant_type")
	}
}

func (h *OIDCHandler) writeTokenError(w http.ResponseWriter, errCode string) {
	h.writeJSON(w, http.StatusBadRequest, tokenErrorResponse{Error: errCode})
}

func (h *OIDCHandler) writeJSON(w http.ResponseWriter, statusCode int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		h.logger.Error("failed to encode OIDC stub response", "error", err)
	}
}
