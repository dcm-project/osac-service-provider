// Package apiserver provides the HTTP server and middleware for the OSAC
// service provider API.
//
// Implements Topic 4.1 (HTTP Server) of the Milestone 1 spec.
package apiserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"time"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	oapigen "github.com/dcm-project/osac-service-provider/internal/api/server"
	"github.com/dcm-project/osac-service-provider/internal/config"
	"github.com/dcm-project/osac-service-provider/internal/httperror"
	"github.com/dcm-project/osac-service-provider/internal/util"
	"github.com/go-chi/chi/v5"
)

// healthPath is used only for this server's own internal startup
// readiness self-probe (waitForReady) — either of the two health paths
// exposed externally works equally well for that purpose, since both
// report the same underlying condition (DD-010, REQ-HLT-015).
const healthPath = "/api/v1alpha1/clusters/health"

// readinessProbeTimeout bounds a single readiness-probing window (see
// waitForReady). It does not bound the overall wait before onReady fires:
// waitForReadyUntilCancelled retries fresh windows indefinitely, so an
// elapsed window alone never permanently skips onReady — only context
// cancellation does (DD-091).
const readinessProbeTimeout = 5 * time.Second

// readinessProbeInterval is the polling interval for the self-probe that
// checks the health endpoint before firing onReady.
const readinessProbeInterval = 50 * time.Millisecond

// Server is the HTTP server for the OSAC service provider API.
type Server struct {
	cfg     *config.Config
	logger  *slog.Logger
	srv     *http.Server
	onReady func(context.Context)

	readinessTimeout  time.Duration
	readinessInterval time.Duration
}

// Option configures a Server. Exported for tests (e.g. WithReadinessTiming)
// — mirrors the same test-seam convention as internal/osac.Bootstrap's
// WithNow/WithMaxBackoff.
type Option func(*Server)

// WithReadinessTiming overrides the timeout/interval waitForReady uses to
// poll the server's own health endpoint before firing onReady. Intended for
// tests that need to exercise waitForReady's timeout/cancellation branches
// without waiting the real (5s) production timeout.
func WithReadinessTiming(timeout, interval time.Duration) Option {
	return func(s *Server) {
		s.readinessTimeout = timeout
		s.readinessInterval = interval
	}
}

// New creates a new Server with the given config, logger, and generated
// ServerInterface implementation.
func New(cfg *config.Config, logger *slog.Logger, handler oapigen.ServerInterface, opts ...Option) *Server {
	badReq := newBadRequestHandler(logger)

	r := chi.NewRouter()
	r.Use(recoveryMiddleware(logger))
	r.Use(requestLoggingMiddleware(logger))
	r.Use(requestTimeoutMiddleware(cfg.Server.RequestTimeout))

	httpHandler := oapigen.HandlerWithOptions(handler, oapigen.ChiServerOptions{
		BaseRouter:       r,
		ErrorHandlerFunc: badReq,
	})

	s := &Server{
		cfg:    cfg,
		logger: logger,
		srv: &http.Server{
			Handler: httpHandler,
		},
		readinessTimeout:  readinessProbeTimeout,
		readinessInterval: readinessProbeInterval,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// WithOnReady registers a callback invoked once the server is confirmed to
// be serving HTTP requests. The server verifies readiness by polling its own
// health endpoint before calling fn. Use this to trigger work (e.g.
// registration) that must wait until the HTTP server is ready
// (REQ-REG-050/AC-REG-030).
func (s *Server) WithOnReady(fn func(context.Context)) *Server {
	s.onReady = fn
	return s
}

// newBadRequestHandler returns a handler that writes a 400 Bad Request
// response with an RFC 9457 application/problem+json body.
func newBadRequestHandler(logger *slog.Logger) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		httperror.WriteResponse(w, logger, http.StatusBadRequest, v1alpha1.INVALIDARGUMENT, "Bad Request", err.Error(), requestInstance(r))
	}
}

// NewRequestErrorHandler returns an error handler for the strict adapter's
// RequestErrorHandlerFunc that writes an RFC 9457 INVALID_ARGUMENT response.
func NewRequestErrorHandler(logger *slog.Logger) func(http.ResponseWriter, *http.Request, error) {
	return newBadRequestHandler(logger)
}

// NewResponseErrorHandler returns an error handler for the strict adapter's
// ResponseErrorHandlerFunc that writes an RFC 9457 INTERNAL response without
// exposing implementation details.
func NewResponseErrorHandler(logger *slog.Logger) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		logger.Error("strict handler response error", "error", err)
		httperror.WriteResponse(w, logger, http.StatusInternalServerError, v1alpha1.INTERNAL, httperror.InternalTitle, httperror.InternalDetail, requestInstance(r))
	}
}

func requestInstance(r *http.Request) *string {
	if r == nil {
		return nil
	}
	return util.Ptr(r.URL.RequestURI())
}

// statusRecordingResponseWriter wraps an http.ResponseWriter to track
// whether headers have already been sent and the response status code.
type statusRecordingResponseWriter struct {
	http.ResponseWriter
	wroteHeader bool
	statusCode  int
}

func (w *statusRecordingResponseWriter) WriteHeader(code int) {
	w.wroteHeader = true
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusRecordingResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.statusCode = http.StatusOK
	}
	w.wroteHeader = true
	return w.ResponseWriter.Write(b)
}

func (w *statusRecordingResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// recoveryMiddleware catches panics and returns an RFC 9457
// application/problem+json response instead of a plain-text stack trace.
//
// Implements REQ-HTTP-070. Must be the outermost middleware.
func recoveryMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sw := &statusRecordingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
			defer func() {
				if rec := recover(); rec != nil {
					if rec == http.ErrAbortHandler { //nolint:errorlint // sentinel comparison per net/http convention
						panic(http.ErrAbortHandler)
					}

					logger.Error("panic recovered", "panic", rec, "stack", string(debug.Stack()))

					if sw.wroteHeader {
						logger.Warn("headers already sent, cannot write RFC 9457 response")
						return
					}

					httperror.WriteResponse(w, logger, http.StatusInternalServerError, v1alpha1.INTERNAL, httperror.InternalTitle, httperror.InternalDetail, requestInstance(r))
				}
			}()
			next.ServeHTTP(sw, r)
		})
	}
}

// requestTimeoutMiddleware cancels the request context after the configured
// timeout. A zero timeout disables the middleware.
//
// Implements REQ-HTTP-090.
func requestTimeoutMiddleware(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if timeout <= 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// requestLoggingMiddleware logs each HTTP request at INFO level with method,
// path, response status code, and duration.
//
// Implements REQ-HTTP-060.
func requestLoggingMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusRecordingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
			next.ServeHTTP(sw, r)
			logger.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.statusCode,
				"duration", time.Since(start).String(),
			)
		})
	}
}

// waitForReadyUntilCancelled repeatedly runs waitForReady's bounded polling
// window, retrying whenever one window elapses without success, until
// either a window succeeds or ctx is cancelled (REQ-REG-052/AC-REG-031).
//
// A single elapsed window is not itself evidence the server will never
// become ready — under transient CPU contention (observed in the kind-based
// e2e infra, see DD-091) a cold-starting pod's /health responses can be slow
// enough to exceed one readinessProbeTimeout window while the server is
// otherwise healthy and about to succeed. Treating that as permanent and
// skipping onReady forever would silently and irrecoverably prevent
// registration for the rest of the process's life, which is worse than
// retrying: ctx cancellation (real shutdown) remains the only true stopping
// condition, consistent with every other retry loop in this codebase
// (internal/osac's OIDC token fetch and gRPC dial).
func (s *Server) waitForReadyUntilCancelled(ctx context.Context, addr string) error {
	for {
		err := s.waitForReady(ctx, addr)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return err
		}
		s.logger.Warn("readiness probe window elapsed, retrying", "error", err)

		// Paces retries so a deterministic, immediately-failing waitForReady
		// error (e.g. TC-U-089's malformed-address case) can't spin this
		// loop tightly; also lets ctx cancellation interrupt the wait
		// promptly rather than only being observed at the next window.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.readinessInterval):
		}
	}
}

// waitForReady polls the server's health endpoint until it returns a
// response or the context/timeout expires.
func (s *Server) waitForReady(ctx context.Context, addr string) error {
	url := fmt.Sprintf("http://%s%s", addr, healthPath)
	client := &http.Client{Timeout: 1 * time.Second}

	deadline := time.NewTimer(s.readinessTimeout)
	defer deadline.Stop()

	ticker := time.NewTicker(s.readinessInterval)
	defer ticker.Stop()

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
		if err != nil {
			return fmt.Errorf("creating readiness probe request: %w", err)
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("server readiness probe timed out after %s", s.readinessTimeout)
		case <-ticker.C:
		}
	}
}

// Run starts the HTTP server on the provided listener and blocks until the
// context is cancelled. Signal handling is the caller's responsibility; pass
// a context that is cancelled on SIGTERM/SIGINT (REQ-HTTP-030/040).
func (s *Server) Run(ctx context.Context, ln net.Listener) error {
	s.logger.Info("server starting", "address", ln.Addr().String())

	serveCh := make(chan error, 1)
	go func() {
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveCh <- err
		}
		close(serveCh)
	}()

	if s.onReady != nil {
		if err := s.waitForReadyUntilCancelled(ctx, ln.Addr().String()); err != nil {
			s.logger.Error("readiness probe failed, skipping onReady callback", "error", err)
		} else {
			func() {
				defer func() {
					if r := recover(); r != nil {
						s.logger.Error("onReady callback panicked", "panic", r)
					}
				}()
				s.onReady(ctx)
			}()
		}
	}

	select {
	case <-ctx.Done():
	case err := <-serveCh:
		if err != nil {
			return fmt.Errorf("serving on %s: %w", ln.Addr(), err)
		}
	}

	s.logger.Info("shutting down server")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), s.cfg.Server.ShutdownTimeout)
	defer shutdownCancel()

	if err := s.srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutting down server: %w", err)
	}
	return nil
}
