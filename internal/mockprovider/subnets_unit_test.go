package mockprovider_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/dcm-project/osac-service-provider/internal/mockprovider"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

var _ = Describe("SubnetsServer", func() {
	var (
		ctx context.Context
		srv *mockprovider.SubnetsServer
	)

	BeforeEach(func() {
		ctx = context.Background()
		srv = mockprovider.NewSubnetsServer()
	})

	// TC-U-126: Create always generates a fresh id, ignoring any
	// caller-supplied value, and sets SUBNET_STATE_READY
	It("always generates a fresh id on Create, ignoring any caller-supplied value, and sets SUBNET_STATE_READY (TC-U-126)", func() {
		resp1, err := srv.Create(ctx, &publicv1.SubnetsCreateRequest{Object: &publicv1.Subnet{Id: ""}})
		Expect(err).NotTo(HaveOccurred())
		Expect(resp1.GetObject().GetId()).NotTo(BeEmpty())
		Expect(resp1.GetObject().GetStatus().GetState()).To(Equal(publicv1.SubnetState_SUBNET_STATE_READY))

		resp2, err := srv.Create(ctx, &publicv1.SubnetsCreateRequest{Object: &publicv1.Subnet{Id: "caller-supplied"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(resp2.GetObject().GetId()).NotTo(BeEmpty())
		Expect(resp2.GetObject().GetId()).NotTo(Equal("caller-supplied"))
		Expect(resp2.GetObject().GetId()).NotTo(Equal(resp1.GetObject().GetId()))
		Expect(resp2.GetObject().GetStatus().GetState()).To(Equal(publicv1.SubnetState_SUBNET_STATE_READY))
	})

	// TC-U-128: Get round-trips a created object; unknown id is NotFound
	It("round-trips a created object via Get; unknown id is NotFound (TC-U-128)", func() {
		createResp, err := srv.Create(ctx, &publicv1.SubnetsCreateRequest{Object: &publicv1.Subnet{}})
		Expect(err).NotTo(HaveOccurred())
		id := createResp.GetObject().GetId()

		getResp, err := srv.Get(ctx, &publicv1.SubnetsGetRequest{Id: id})
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(getResp.GetObject(), createResp.GetObject())).To(BeTrue())

		_, err = srv.Get(ctx, &publicv1.SubnetsGetRequest{Id: "missing"})
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.NotFound))
	})

	// TC-U-130: List reflects all created objects
	It("reflects all created objects on List (TC-U-130)", func() {
		resp1, err := srv.Create(ctx, &publicv1.SubnetsCreateRequest{Object: &publicv1.Subnet{}})
		Expect(err).NotTo(HaveOccurred())
		resp2, err := srv.Create(ctx, &publicv1.SubnetsCreateRequest{Object: &publicv1.Subnet{}})
		Expect(err).NotTo(HaveOccurred())

		listResp, err := srv.List(ctx, &publicv1.SubnetsListRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(listResp.GetSize()).To(Equal(int32(2)))
		Expect(listResp.GetTotal()).To(Equal(int32(2)))
		ids := []string{listResp.GetItems()[0].GetId(), listResp.GetItems()[1].GetId()}
		Expect(ids).To(ConsistOf(resp1.GetObject().GetId(), resp2.GetObject().GetId()))
	})

	// TC-U-132: Delete removes a known id; a second Delete is NotFound
	It("deletes a known id and returns NotFound on a second Delete (TC-U-132)", func() {
		createResp, err := srv.Create(ctx, &publicv1.SubnetsCreateRequest{Object: &publicv1.Subnet{}})
		Expect(err).NotTo(HaveOccurred())
		id := createResp.GetObject().GetId()

		_, err = srv.Delete(ctx, &publicv1.SubnetsDeleteRequest{Id: id})
		Expect(err).NotTo(HaveOccurred())

		listResp, err := srv.List(ctx, &publicv1.SubnetsListRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(listResp.GetItems()).To(BeEmpty())

		_, err = srv.Delete(ctx, &publicv1.SubnetsDeleteRequest{Id: id})
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.NotFound))
	})
})
