package mockprovider_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMockProvider(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Mock Provider Suite")
}
