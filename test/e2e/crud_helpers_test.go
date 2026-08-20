package e2e_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/gomega"
)

// Shared helpers for cluster_crud_test.go / vm_crud_test.go (TC-E2E-090..101).
// Neither file imports the main module's generated api/v1alpha1 types —
// same rationale as health_test.go's own health struct: keeps test/e2e's
// module (REQ-E2E-080) independent of the parent module.

// osacRequest issues a real HTTP request against osac-sp, returning the
// status code and the fully-read, closed response body for the caller to
// assert on/decode.
func osacRequest(method, path, body string) (int, []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, osacSPURLJoin(path), reader)
	Expect(err).NotTo(HaveOccurred())
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())
	return resp.StatusCode, respBody
}

// uniqueID returns a fresh id for this suite run, avoiding collisions
// between specs that share the same live osac-mock-provider in-memory
// store for the whole job (REQ-MOCK-020's ALREADY_EXISTS behavior would
// otherwise make a rerun/re-declaration-order-sensitive test flaky).
func uniqueID(prefix string) string {
	b := make([]byte, 4)
	_, err := rand.Read(b)
	Expect(err).NotTo(HaveOccurred())
	return prefix + "-" + hex.EncodeToString(b)
}

// assertValidBase64 fails the current spec if s isn't valid base64 — used
// to check a kubeconfig field without asserting on its (mock, opaque)
// decoded content.
func assertValidBase64(s string) {
	_, err := base64.StdEncoding.DecodeString(s)
	Expect(err).NotTo(HaveOccurred(), "expected valid base64, got: %s", s)
}
