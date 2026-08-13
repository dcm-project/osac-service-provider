package apiserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	oapigen "github.com/dcm-project/osac-service-provider/internal/api/server"
	"github.com/dcm-project/osac-service-provider/internal/apiserver"
	"github.com/dcm-project/osac-service-provider/internal/config"
	"github.com/dcm-project/osac-service-provider/internal/httperror"
)

// fakeServerInterface satisfies oapigen.ServerInterface with a swappable
// health implementation shared by both routes (DD-010/REQ-HLT-015), per the
// unit test plan's convention of registering handlers directly rather than
// going through the strict adapter/business logic. Embeds oapigen.Unimplemented
// so the Milestone 3 Cluster CRUD methods (out of scope for these
// generic-server-behavior tests) are satisfied with a 501 stub rather than
// needing a duplicate hand-rolled no-op per method.
type fakeServerInterface struct {
	oapigen.Unimplemented
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
	return runServer(apiserver.New(cfg, logger, handler), context.Background())
}

// runServer is startServer's shared implementation, taking an
// already-constructed Server (so tests needing WithOnReady/WithReadinessTiming
// can configure it before starting) and a base context (so tests needing to
// force Run's error paths can pass an already-cancelled one).
func runServer(srv *apiserver.Server, baseCtx context.Context) (baseURL string, stop func()) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())

	ctx, cancel := context.WithCancel(baseCtx)
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
		Expect(body.Type).To(Equal(v1alpha1.ErrorTypeINTERNAL))
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
		Expect(body.Type).To(Equal(v1alpha1.ErrorTypeINTERNAL))
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

var _ = Describe("recoveryMiddleware edge cases", func() {
	discardLogger := slog.New(slog.DiscardHandler)

	// TC-U-087: http.ErrAbortHandler is re-panicked, not converted into
	// an RFC 9457 response — net/http's own server-level recovery treats
	// this sentinel specially (silently aborts the connection), and a
	// handler that deliberately panics with it must not have that intent
	// overridden into a normal-looking 500 response.
	It("re-panics http.ErrAbortHandler instead of writing an RFC 9457 response (TC-U-087)", func() {
		handler := &fakeServerInterface{getHealth: func(_ http.ResponseWriter, _ *http.Request) {
			panic(http.ErrAbortHandler)
		}}
		baseURL, stop := startServer(testServerConfig(0), discardLogger, handler)
		defer stop()

		_, err := http.Get(baseURL + "/api/v1alpha1/clusters/health") //nolint:noctx // test helper
		Expect(err).To(HaveOccurred(), "the connection must be aborted, not answered with a normal RFC 9457 response")

		// The server process itself must survive the abort: the listener
		// keeps accepting new connections. Checked via a fresh TCP dial
		// rather than mutating/reusing handler (which the aborted
		// request's own server-side goroutine may still be unwinding at
		// this point — a data race under -race, since the client
		// observing the abort doesn't synchronize with that goroutine's
		// completion).
		addr := strings.TrimPrefix(baseURL, "http://")
		Eventually(func() error {
			conn, dialErr := net.DialTimeout("tcp", addr, 50*time.Millisecond)
			if dialErr == nil {
				_ = conn.Close()
			}
			return dialErr
		}, "500ms", "5ms").Should(Succeed())
	})

	// TC-U-088: if headers were already sent before the panic, recovery
	// cannot retroactively rewrite them to an RFC 9457 500 — it must log
	// a warning and stop, not attempt (and fail) a second WriteHeader.
	It("logs a warning instead of double-writing when headers were already sent before a panic (TC-U-088)", func() {
		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, nil))
		handler := &fakeServerInterface{getHealth: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			panic("boom after headers sent")
		}}
		baseURL, stop := startServer(testServerConfig(0), logger, handler)
		defer stop()

		resp, err := http.Get(baseURL + "/api/v1alpha1/clusters/health") //nolint:noctx // test helper
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		Eventually(buf.String, "200ms", "5ms").Should(ContainSubstring("headers already sent, cannot write RFC 9457 response"))
	})
})

var _ = Describe("WithOnReady / Run readiness integration", func() {
	discardLogger := slog.New(slog.DiscardHandler)
	okHandler := &fakeServerInterface{getHealth: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }}

	// TC-U-074: the onReady callback fires exactly once, only after the
	// server is confirmed ready (REQ-REG-050/AC-REG-030) — not zero times
	// (registration would never start) and not more than once (would
	// double-register).
	It("invokes onReady exactly once, after real readiness (TC-U-074)", func() {
		var calls int32
		srv := apiserver.New(testServerConfig(0), discardLogger, okHandler).WithOnReady(func(context.Context) {
			atomic.AddInt32(&calls, 1)
		})
		_, stop := runServer(srv, context.Background())
		defer stop()

		Eventually(func() int32 { return atomic.LoadInt32(&calls) }, "500ms", "5ms").Should(Equal(int32(1)))
		Consistently(func() int32 { return atomic.LoadInt32(&calls) }, "50ms", "5ms").Should(Equal(int32(1)))
	})

	// TC-U-075: a panicking onReady callback is recovered — it must not
	// crash Run or prevent the server from serving requests normally.
	It("recovers a panicking onReady callback and keeps serving (TC-U-075)", func() {
		srv := apiserver.New(testServerConfig(0), discardLogger, okHandler).WithOnReady(func(context.Context) {
			panic("boom from onReady")
		})
		baseURL, stop := runServer(srv, context.Background())
		defer stop()

		Eventually(func() (int, error) {
			resp, err := http.Get(baseURL + "/api/v1alpha1/clusters/health") //nolint:noctx // test helper
			if err != nil {
				return 0, err
			}
			defer func() { _ = resp.Body.Close() }()
			return resp.StatusCode, nil
		}, "500ms", "5ms").Should(Equal(http.StatusOK))
	})

	// TC-U-076: a failed readiness probe skips onReady (no registration
	// attempt against a server that isn't actually confirmed serving)
	// without hanging Run.
	It("skips onReady when the readiness probe fails, without hanging (TC-U-076)", func() {
		var called int32
		srv := apiserver.New(testServerConfig(0), discardLogger, okHandler).WithOnReady(func(context.Context) {
			atomic.AddInt32(&called, 1)
		})

		ln, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // already cancelled before Run starts, forcing waitForReady to fail fast

		runErr := make(chan error, 1)
		go func() { runErr <- srv.Run(ctx, ln) }()

		Eventually(runErr, "500ms").Should(Receive())
		Expect(atomic.LoadInt32(&called)).To(Equal(int32(0)))
	})

	// TC-U-080: a genuine Serve error (not the expected http.ErrServerClosed
	// from a graceful shutdown) is surfaced as Run's return error, not
	// silently swallowed.
	It("surfaces a genuine Serve error as Run's return error (TC-U-080)", func() {
		srv := apiserver.New(testServerConfig(0), discardLogger, okHandler)

		ln, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		Expect(ln.Close()).To(Succeed()) // closed before Run ever calls Serve on it

		runErr := srv.Run(context.Background(), ln)
		Expect(runErr).To(HaveOccurred())
		Expect(runErr.Error()).To(ContainSubstring("serving on"))
	})

	// TC-U-081: a Shutdown that cannot finish within ShutdownTimeout (a
	// real in-flight request still being served) is surfaced as Run's
	// return error, per REQ-HTTP-040.
	It("surfaces a Shutdown timeout as Run's return error (TC-U-081)", func() {
		reqStarted := make(chan struct{})
		slowHandler := &fakeServerInterface{getHealth: func(w http.ResponseWriter, _ *http.Request) {
			close(reqStarted)
			time.Sleep(300 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}}
		cfg := testServerConfig(0)
		cfg.Server.ShutdownTimeout = 1 * time.Millisecond
		srv := apiserver.New(cfg, discardLogger, slowHandler)

		ln, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithCancel(context.Background())
		runErr := make(chan error, 1)
		go func() { runErr <- srv.Run(ctx, ln) }()

		Eventually(func() error {
			conn, err := net.DialTimeout("tcp", ln.Addr().String(), 50*time.Millisecond)
			if err == nil {
				_ = conn.Close()
			}
			return err
		}, "500ms", "5ms").Should(Succeed())

		go func() {
			resp, err := http.Get("http://" + ln.Addr().String() + "/api/v1alpha1/clusters/health") //nolint:noctx // test helper
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		Eventually(reqStarted, "500ms").Should(BeClosed())
		cancel()

		var gotErr error
		Eventually(runErr, "1s").Should(Receive(&gotErr))
		Expect(gotErr).To(HaveOccurred())
		Expect(gotErr.Error()).To(ContainSubstring("shutting down server"))
	})
})

var _ = Describe("Strict-adapter error handlers", func() {
	discardLogger := slog.New(slog.DiscardHandler)

	// TC-U-082: newBadRequestHandler / NewRequestErrorHandler writes an
	// RFC 9457 INVALID_ARGUMENT response carrying the request's exact URI
	// as instance.
	It("NewRequestErrorHandler writes an RFC 9457 INVALID_ARGUMENT response (TC-U-082)", func() {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1alpha1/clusters/health?bad=param", http.NoBody)

		apiserver.NewRequestErrorHandler(discardLogger)(w, r, errors.New("invalid query param"))

		Expect(w.Code).To(Equal(http.StatusBadRequest))
		Expect(w.Header().Get("Content-Type")).To(Equal("application/problem+json"))

		var body v1alpha1.Error
		Expect(json.NewDecoder(w.Body).Decode(&body)).To(Succeed())
		Expect(body.Type).To(Equal(v1alpha1.ErrorTypeINVALIDARGUMENT))
		Expect(*body.Instance).To(Equal("/api/v1alpha1/clusters/health?bad=param"))
	})

	// TC-U-083: NewResponseErrorHandler writes an RFC 9457 INTERNAL
	// response using the generic detail constant, never the raw
	// underlying error string (which could leak implementation details).
	It("NewResponseErrorHandler writes a generic RFC 9457 INTERNAL response without leaking the error (TC-U-083)", func() {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1alpha1/clusters/health", http.NoBody)

		apiserver.NewResponseErrorHandler(discardLogger)(w, r, errors.New("sensitive: db connection string leaked here"))

		Expect(w.Code).To(Equal(http.StatusInternalServerError))

		var body v1alpha1.Error
		Expect(json.NewDecoder(w.Body).Decode(&body)).To(Succeed())
		Expect(body.Type).To(Equal(v1alpha1.ErrorTypeINTERNAL))
		Expect(*body.Detail).To(Equal(httperror.InternalDetail))
		Expect(*body.Detail).NotTo(ContainSubstring("sensitive"))
	})
})

var errNotFound = &notFoundError{}

type notFoundError struct{}

func (*notFoundError) Error() string { return "log record not found yet" }
