package osac

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestOSAC(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "OSAC Bootstrap Suite")
}
