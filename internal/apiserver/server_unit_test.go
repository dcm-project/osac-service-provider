package apiserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	oapigen "github.com/dcm-project/osac-service-provider/internal/api/server"
	"github.com/dcm-project/osac-service-provider/internal/apiserver"
	"github.com/dcm-project/osac-service-provider/internal/config"
)

// fakeServerInterface satisfies oapigen.ServerInterface with a swappable
// health implementation shared by both routes (DD-010/REQ-HLT-015), per the
// unit test plan's convention of registering handlers directly rather than
// going through the strict adapter/business logic.
type fakeServerInterface struct {
	getHealth func(w http.ResponseWriter, r *http.Request)
}

func (f *fakeServerInterface) GetClustersHealth(w http.ResponseWriter, r *http.Request) {
	f.getHealth(w, r)
}

func (f *fakeServerInterface) GetVMsHealth(w http.ResponseWriter, r *http.Request) {
	f.getHealth(w, r)
}

func testServerConfig(requestTimeout time.Duration) *config.Config {
	return &config.Config{
		Server: config.ServerConfig{
			ShutdownTimeout: time.Second,
			RequestTimeout:  requestTimeout,
		},
	}
}

// startServer boots a Server on a loopback listener and returns its base URL
// and a stop function. Using a real listener (rather than exporting an
// internal http.Handler) keeps the test exercising only Server's public API.
func startServer(cfg *config.Config, logger *slog.Logger, handler oapigen.ServerInterface) (baseURL string, stop func()) {
	srv := apiserver.New(cfg, logger, handler)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Run(ctx, ln)
	}()

	// Give Serve a moment to be accepting connections.
	Eventually(func() error {
		conn, err := net.DialTimeout("tcp", ln.Addr().String(), 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
		}
		return err
	}, "500ms", "5ms").Should(Succeed())

	return "http://" + ln.Addr().String(), func() {
		cancel()
		<-done
	}
}

var _ = Describe("Server middleware", func() {
	discardLogger := slog.New(slog.DiscardHandler)

	// TC-U-070: panic in a handler returns an RFC 9457 INTERNAL response.
	It("converts a handler panic into an RFC 9457 INTERNAL response (TC-U-070)", func() {
		handler := &fakeServerInterface{getHealth: func(_ http.ResponseWriter, _ *http.Request) {
			panic("boom")
		}}
		baseURL, stop := startServer(testServerConfig(0), discardLogger, handler)
		defer stop()

		resp, err := http.Get(baseURL + "/api/v1alpha1/clusters/health") //nolint:noctx // test helper
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()

		Expect(resp.StatusCode).To(Equal(http.StatusInternalServerError))
		Expect(resp.Header.Get("Content-Type")).To(Equal("application/problem+json"))

		var body v1alpha1.Error
		Expect(json.NewDecoder(resp.Body).Decode(&body)).To(Succeed())
		Expect(body.Type).To(Equal(v1alpha1.INTERNAL))
		Expect(string(body.Type)).To(Equal("https://dcm-project.github.io/problems/internal"),
			"type must be the RFC 9457 project-controlled URI, not a bare code")

		// TC-U-073 (adapted): recovery is outermost / robust — the server
		// keeps serving normally after recovering from a panic.
		handler.getHealth = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
		resp2, err := http.Get(baseURL + "/api/v1alpha1/clusters/health") //nolint:noctx // test helper
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = resp2.Body.Close() }()
		Expect(resp2.StatusCode).To(Equal(http.StatusOK))
	})

	// TC-U-071: request logging captures method/path/status/duration.
	It("logs method, path, status, and duration for each request (TC-U-071)", func() {
		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, nil))

		handler := &fakeServerInterface{getHealth: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		}}
		baseURL, stop := startServer(testServerConfig(0), logger, handler)
		defer stop()

		resp, err := http.Get(baseURL + "/api/v1alpha1/clusters/health") //nolint:noctx // test helper
		Expect(err).NotTo(HaveOccurred())
		_ = resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusTeapot))

		var record map[string]any
		Eventually(func() error {
			buf2 := buf.String()
			for _, line := range bytes.Split([]byte(buf2), []byte("\n")) {
				if len(line) == 0 {
					continue
				}
				var r map[string]any
				if err := json.Unmarshal(line, &r); err == nil {
					if r["msg"] == "http request" {
						record = r
						return nil
					}
				}
			}
			return errNotFound
		}, "500ms", "5ms").Should(Succeed())

		Expect(record["method"]).To(Equal("GET"))
		Expect(record["path"]).To(Equal("/api/v1alpha1/clusters/health"))
		Expect(record["status"]).To(BeNumerically("==", http.StatusTeapot))
		Expect(record["duration"]).NotTo(BeEmpty())
	})

	// TC-U-072: request timeout cancels the handler's context.
	It("cancels the request context once the configured timeout elapses (TC-U-072)", func() {
		var observedErr atomic.Value // stores error
		done := make(chan struct{})

		handler := &fakeServerInterface{getHealth: func(_ http.ResponseWriter, r *http.Request) {
			time.Sleep(100 * time.Millisecond)
			observedErr.Store(r.Context().Err())
			close(done)
		}}
		baseURL, stop := startServer(testServerConfig(10*time.Millisecond), discardLogger, handler)
		defer stop()

		resp, err := http.Get(baseURL + "/api/v1alpha1/clusters/health") //nolint:noctx // test helper
		if err == nil {
			_ = resp.Body.Close()
		}

		Eventually(done, "500ms").Should(BeClosed())
		errVal, _ := observedErr.Load().(error)
		Expect(errVal).To(MatchError(context.DeadlineExceeded))
	})

	// TC-U-073: recovery sits outermost in the real middleware chain. Unlike
	// TC-U-070 (which uses testServerConfig(0), a timeout of 0 disables
	// requestTimeoutMiddleware entirely — see its `if timeout <= 0 { return
	// next }` passthrough), this case configures a real nonzero timeout so
	// all three middlewares (recovery, logging, timeout) are genuinely
	// active, and confirms a panic from the terminal handler still
	// propagates all the way up through both of them to be caught by
	// recovery — not silently absorbed by logging's post-ServeHTTP Info()
	// call, which would happen if logging were positioned outside recovery.
	It("catches a handler panic through the full active middleware stack, proving recovery sits outermost (TC-U-073)", func() {
		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, nil))

		handler := &fakeServerInterface{getHealth: func(_ http.ResponseWriter, _ *http.Request) {
			panic("boom from deep in the stack")
		}}
		baseURL, stop := startServer(testServerConfig(time.Second), logger, handler)
		defer stop()

		resp, err := http.Get(baseURL + "/api/v1alpha1/clusters/health") //nolint:noctx // test helper
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()

		Expect(resp.StatusCode).To(Equal(http.StatusInternalServerError))
		Expect(resp.Header.Get("Content-Type")).To(Equal("application/problem+json"))

		var body v1alpha1.Error
		Expect(json.NewDecoder(resp.Body).Decode(&body)).To(Succeed())
		Expect(body.Type).To(Equal(v1alpha1.INTERNAL))
		Expect(string(body.Type)).To(Equal("https://dcm-project.github.io/problems/internal"),
			"type must be the RFC 9457 project-controlled URI, not a bare code")

		// If recovery were not outermost, the panic could instead be
		// observed by requestLoggingMiddleware's post-ServeHTTP Info()
		// call. Assert no "http request" log line was ever written for
		// this (panicking) request.
		Consistently(func() bool {
			return bytes.Contains(buf.Bytes(), []byte(`"msg":"http request"`))
		}, "20ms", "5ms").Should(BeFalse())
	})
})

var errNotFound = &notFoundError{}

type notFoundError struct{}

func (*notFoundError) Error() string { return "log record not found yet" }
