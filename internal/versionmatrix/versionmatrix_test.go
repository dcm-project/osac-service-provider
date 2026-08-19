package versionmatrix_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestVersionMatrix(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "VersionMatrix Suite")
}
