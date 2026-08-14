package httperror_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	"github.com/dcm-project/osac-service-provider/internal/httperror"
	"github.com/dcm-project/osac-service-provider/internal/util"
)

// failingResponseWriter wraps an httptest.ResponseRecorder but fails every
// Write call, so tests can exercise WriteResponse's json-encode-failure
// branch (TC-U-092) without a real broken network connection.
type failingResponseWriter struct {
	*httptest.ResponseRecorder
}

func (w *failingResponseWriter) Write([]byte) (int, error) {
	return 0, errors.New("simulated write failure")
}

var _ = Describe("WriteResponse", func() {
	// TC-U-090: writes the exact status/headers/body fields given.
	It("writes exact status/headers/body fields (TC-U-090)", func() {
		w := httptest.NewRecorder()
		logger := slog.New(slog.DiscardHandler)

		httperror.WriteResponse(w, logger, http.StatusBadRequest, v1alpha1.ErrorTypeINVALIDARGUMENT, "Bad Request", "the request was malformed", util.Ptr("/api/v1alpha1/clusters/health"))

		Expect(w.Code).To(Equal(http.StatusBadRequest))
		Expect(w.Header().Get("Content-Type")).To(Equal("application/problem+json"))

		var body v1alpha1.Error
		Expect(json.NewDecoder(w.Body).Decode(&body)).To(Succeed())
		Expect(body.Type).To(Equal(v1alpha1.ErrorTypeINVALIDARGUMENT))
		Expect(body.Title).To(Equal("Bad Request"))
		Expect(*body.Status).To(Equal(int32(http.StatusBadRequest)))
		Expect(*body.Detail).To(Equal("the request was malformed"))
		Expect(*body.Instance).To(Equal("/api/v1alpha1/clusters/health"))
	})

	// TC-U-091: a nil instance is omitted from the encoded body, not
	// encoded as an empty string.
	It("omits a nil instance from the encoded body (TC-U-091)", func() {
		w := httptest.NewRecorder()
		logger := slog.New(slog.DiscardHandler)

		httperror.WriteResponse(w, logger, http.StatusInternalServerError, v1alpha1.ErrorTypeINTERNAL, httperror.InternalTitle, httperror.InternalDetail, nil)

		var raw map[string]any
		Expect(json.Unmarshal(w.Body.Bytes(), &raw)).To(Succeed())
		_, present := raw["instance"]
		Expect(present).To(BeFalse(), "instance must be omitted, not present as an empty/null value")

		var body v1alpha1.Error
		Expect(json.Unmarshal(w.Body.Bytes(), &body)).To(Succeed())
		Expect(body.Instance).To(BeNil())
	})

	// TC-U-092: an encode failure (write error) is logged, not panicked.
	It("logs, and does not panic, when the underlying writer fails (TC-U-092)", func() {
		var logBuf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
		w := &failingResponseWriter{ResponseRecorder: httptest.NewRecorder()}

		Expect(func() {
			httperror.WriteResponse(w, logger, http.StatusInternalServerError, v1alpha1.ErrorTypeINTERNAL, httperror.InternalTitle, httperror.InternalDetail, nil)
		}).NotTo(Panic())

		Expect(logBuf.String()).To(ContainSubstring("failed to encode error response"))
	})
})
