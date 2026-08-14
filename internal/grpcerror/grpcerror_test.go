package grpcerror_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestGRPCError(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "GRPC Error Classification Suite")
}
