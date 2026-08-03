package httperror_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestHTTPError(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "HTTP Error Suite")
}
