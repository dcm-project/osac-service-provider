package mockprovider_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/dcm-project/osac-service-provider/internal/mockprovider"
)

// newOIDCTestServer starts a real httptest.Server hosting a
// mockprovider.OIDCHandler whose discovery documents advertise that same
// server's own /token path — mirroring how cmd/osac-mock-provider must
// construct it from a real net.Listen address (the token URL can't be
// known until the listener exists).
func newOIDCTestServer() *httptest.Server {
	ts := httptest.NewUnstartedServer(nil)
	tokenURL := "http://" + ts.Listener.Addr().String() + "/token"
	ts.Config.Handler = mockprovider.NewOIDCHandler(tokenURL)
	ts.Start()
	return ts
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
		defer resp.Body.Close()
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
		defer resp.Body.Close()
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
		defer resp.Body.Close()
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
		defer resp.Body.Close()
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
		defer resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))

		var body struct {
			Error string `json:"error"`
		}
		Expect(json.NewDecoder(resp.Body).Decode(&body)).To(Succeed())
		Expect(body.Error).NotTo(BeEmpty())
	})
})
