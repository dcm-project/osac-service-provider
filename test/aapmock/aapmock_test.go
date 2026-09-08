package aapmock_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAAPMock(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "AAP Mock Suite")
}
