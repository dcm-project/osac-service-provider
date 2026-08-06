package statuspublisher

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestStatusPublisher(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "StatusPublisher Suite")
}
