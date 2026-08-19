package statuspoll

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestStatusPoll(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "StatusPoll Suite")
}
