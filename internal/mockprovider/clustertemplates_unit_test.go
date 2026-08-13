package mockprovider_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dcm-project/osac-service-provider/internal/mockprovider"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

var _ = Describe("ClusterTemplatesServer", func() {
	var (
		ctx context.Context
		srv *mockprovider.ClusterTemplatesServer
	)

	BeforeEach(func() {
		ctx = context.Background()
		srv = mockprovider.NewClusterTemplatesServer()
	})

	// TC-U-156: Get resolves the well-known default-hcp template with
	// exactly one node set; an unknown id is NotFound.
	It("resolves the well-known default-hcp template with exactly one node set; unknown id is NotFound (TC-U-156)", func() {
		resp, err := srv.Get(ctx, &publicv1.ClusterTemplatesGetRequest{Id: "default-hcp"})
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.GetObject().GetNodeSets()).To(HaveLen(1))

		_, err = srv.Get(ctx, &publicv1.ClusterTemplatesGetRequest{Id: "missing"})
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.NotFound))
	})
})
