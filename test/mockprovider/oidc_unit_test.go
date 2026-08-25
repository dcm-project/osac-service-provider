package mockprovider_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/dcm-project/osac-service-provider/test/mockprovider"
)

// failingResponseWriter wraps an httptest.ResponseRecorder but fails every
// Write call, so a test can exercise the OIDCHandler's json-encode-failure
// branch (TC-U-141) without a real broken network connection — same
// pattern as internal/httperror/write_unit_test.go's TC-U-092.
type failingResponseWriter struct {
	*httptest.ResponseRecorder
}

func (w *failingResponseWriter) Write([]byte) (int, error) {
	return 0, errors.New("simulated write failure")
}

// newOIDCTestServer starts a real httptest.Server hosting a
// mockprovider.OIDCHandler. Its discovery documents advertise a
// token_endpoint built from each request's own Host header (see
// OIDCHandler's doc comment and DD-139), which for a plain http.Client
// request against this server is exactly ts.URL's host:port — so
// TC-U-135/136 below can still assert against ts.URL+"/token" verbatim.
func newOIDCTestServer() *httptest.Server {
	return httptest.NewServer(mockprovider.NewOIDCHandler(slog.New(slog.DiscardHandler)))
}

var _ = Describe("OIDCHandler", func() {
	var ts *httptest.Server

	BeforeEach(func() {
		ts = newOIDCTestServer()
	})

	AfterEach(func() {
		ts.Close()
	})

	// TC-U-135: oauth-authorization-server discovery document resolves to
	// the token endpoint
	It("serves the oauth-authorization-server discovery document with the correct token_endpoint (TC-U-135)", func() {
		resp, err := http.Get(ts.URL + "/.well-known/oauth-authorization-server")
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		var doc struct {
			TokenEndpoint string `json:"token_endpoint"`
		}
		Expect(json.NewDecoder(resp.Body).Decode(&doc)).To(Succeed())
		Expect(doc.TokenEndpoint).To(Equal(ts.URL + "/token"))
	})

	// TC-U-136: openid-configuration discovery document resolves to the
	// token endpoint
	It("serves the openid-configuration discovery document with the correct token_endpoint (TC-U-136)", func() {
		resp, err := http.Get(ts.URL + "/.well-known/openid-configuration")
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		var doc struct {
			TokenEndpoint string `json:"token_endpoint"`
		}
		Expect(json.NewDecoder(resp.Body).Decode(&doc)).To(Succeed())
		Expect(doc.TokenEndpoint).To(Equal(ts.URL + "/token"))
	})

	// TC-U-137: token endpoint issues a bearer token for a
	// client_credentials grant
	It("issues a bearer token for a client_credentials grant (TC-U-137)", func() {
		form := url.Values{"grant_type": {"client_credentials"}}
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/token", strings.NewReader(form.Encode()))
		Expect(err).NotTo(HaveOccurred())
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetBasicAuth("some-client-id", "some-client-secret")

		resp, err := http.DefaultClient.Do(req)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		var tok struct {
			AccessToken string `json:"access_token"`
			TokenType   string `json:"token_type"`
			ExpiresIn   int    `json:"expires_in"`
		}
		Expect(json.NewDecoder(resp.Body).Decode(&tok)).To(Succeed())
		Expect(tok.AccessToken).NotTo(BeEmpty())
		Expect(tok.TokenType).To(Equal("Bearer"))
		Expect(tok.ExpiresIn).To(BeNumerically(">", 0))
	})

	// TC-U-138: token endpoint rejects a non-client_credentials grant type
	It("rejects a non-client_credentials grant type (TC-U-138)", func() {
		form := url.Values{"grant_type": {"authorization_code"}}
		resp, err := http.Post(ts.URL+"/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))

		var body struct {
			Error string `json:"error"`
		}
		Expect(json.NewDecoder(resp.Body).Decode(&body)).To(Succeed())
		Expect(body.Error).NotTo(BeEmpty())
	})

	// TC-U-139: token endpoint rejects a request with no grant_type at all
	It("rejects a request with no grant_type at all (TC-U-139)", func() {
		resp, err := http.Post(ts.URL+"/token", "application/x-www-form-urlencoded", strings.NewReader(""))
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))

		var body struct {
			Error string `json:"error"`
		}
		Expect(json.NewDecoder(resp.Body).Decode(&body)).To(Succeed())
		Expect(body.Error).NotTo(BeEmpty())
	})

	// TC-U-140: token endpoint rejects a request whose form body isn't
	// parseable at all (as opposed to merely missing/wrong grant_type)
	It("rejects a request with an unparseable form body (TC-U-140)", func() {
		resp, err := http.Post(ts.URL+"/token", "application/x-www-form-urlencoded", strings.NewReader("grant_type=%zz"))
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))

		var body struct {
			Error string `json:"error"`
		}
		Expect(json.NewDecoder(resp.Body).Decode(&body)).To(Succeed())
		Expect(body.Error).NotTo(BeEmpty())
	})

	// TC-U-141: an encode failure (write error) is logged, not panicked
	It("logs, and does not panic, when the underlying writer fails (TC-U-141)", func() {
		var logBuf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
		handler := mockprovider.NewOIDCHandler(logger)

		w := &failingResponseWriter{ResponseRecorder: httptest.NewRecorder()}
		req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil)

		Expect(func() {
			handler.ServeHTTP(w, req)
		}).NotTo(Panic())

		Expect(logBuf.String()).To(ContainSubstring("failed to encode OIDC stub response"))
	})

	// TC-U-152: regression test for a real bug found via the kind-based
	// e2e infra (osac-sp-e2e-suite TC-E2E-050/070, DD-139). The mock's
	// OIDC listener binds a wildcard address (":9091") so other pods can
	// reach it, but net.Listener.Addr().String() on a wildcard bind
	// reports the unroutable "[::]:9091" — baking that into the
	// discovery document at construction time made the mock's own token
	// endpoint unreachable from any other pod. The fix derives
	// token_endpoint from each request's own Host header instead.
	It("advertises a token_endpoint built from the request's own Host header, not a fixed address (TC-U-152)", func() {
		handler := mockprovider.NewOIDCHandler(slog.New(slog.DiscardHandler))

		for _, host := range []string{"osac-mock-provider:9091", "127.0.0.1:54321"} {
			req := httptest.NewRequest(http.MethodGet, "/.well-known/openid-configuration", nil)
			req.Host = host
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))
			var doc struct {
				TokenEndpoint string `json:"token_endpoint"`
			}
			Expect(json.NewDecoder(w.Body).Decode(&doc)).To(Succeed())
			Expect(doc.TokenEndpoint).To(Equal("http://" + host + "/token"))
		}
	})
})
