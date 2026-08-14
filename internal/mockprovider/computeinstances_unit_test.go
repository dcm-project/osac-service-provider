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

var _ = Describe("ComputeInstancesServer", func() {
	var (
		ctx context.Context
		srv *mockprovider.ComputeInstancesServer
	)

	BeforeEach(func() {
		ctx = context.Background()
		srv = mockprovider.NewComputeInstancesServer()
	})

	// TC-U-115: Create rejects an empty id
	It("rejects Create with an empty id (TC-U-115)", func() {
		_, err := srv.Create(ctx, &publicv1.ComputeInstancesCreateRequest{
			Object: &publicv1.ComputeInstance{Id: ""},
		})
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.InvalidArgument))

		listResp, err := srv.List(ctx, &publicv1.ComputeInstancesListRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(listResp.GetItems()).To(BeEmpty())
	})

	// TC-U-117: Create rejects a duplicate id
	It("rejects Create with a duplicate id, preserving the original (TC-U-117)", func() {
		_, err := srv.Create(ctx, &publicv1.ComputeInstancesCreateRequest{
			Object: &publicv1.ComputeInstance{Id: "x", Spec: &publicv1.ComputeInstanceSpec{Template: &publicv1.ComputeInstanceTemplateReference{Id: "first"}}},
		})
		Expect(err).NotTo(HaveOccurred())

		_, err = srv.Create(ctx, &publicv1.ComputeInstancesCreateRequest{
			Object: &publicv1.ComputeInstance{Id: "x", Spec: &publicv1.ComputeInstanceSpec{Template: &publicv1.ComputeInstanceTemplateReference{Id: "second"}}},
		})
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.AlreadyExists))

		listResp, err := srv.List(ctx, &publicv1.ComputeInstancesListRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(listResp.GetItems()).To(HaveLen(1))
		Expect(listResp.GetItems()[0].GetSpec().GetTemplate().GetId()).To(Equal("first"))
	})

	// TC-U-119: Create sets COMPUTE_INSTANCE_STATE_RUNNING and round-trips via Get/List
	It("sets COMPUTE_INSTANCE_STATE_RUNNING on Create and round-trips via Get/List (TC-U-119)", func() {
		createResp, err := srv.Create(ctx, &publicv1.ComputeInstancesCreateRequest{
			Object: &publicv1.ComputeInstance{Id: "x"},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(createResp.GetObject().GetStatus().GetState()).To(Equal(publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING))

		getResp, err := srv.Get(ctx, &publicv1.ComputeInstancesGetRequest{Id: "x"})
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(getResp.GetObject(), createResp.GetObject())).To(BeTrue())

		listResp, err := srv.List(ctx, &publicv1.ComputeInstancesListRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(listResp.GetItems()).To(HaveLen(1))
		Expect(proto.Equal(listResp.GetItems()[0], createResp.GetObject())).To(BeTrue())
	})

	// TC-U-121: Get of an unknown id is NotFound
	It("returns NotFound for Get of an unknown id (TC-U-121)", func() {
		_, err := srv.Get(ctx, &publicv1.ComputeInstancesGetRequest{Id: "missing"})
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.NotFound))
	})

	// TC-U-123: List honors offset/limit in creation order
	It("honors offset/limit in creation order on List (TC-U-123)", func() {
		for _, id := range []string{"a", "b", "c"} {
			_, err := srv.Create(ctx, &publicv1.ComputeInstancesCreateRequest{Object: &publicv1.ComputeInstance{Id: id}})
			Expect(err).NotTo(HaveOccurred())
		}

		listResp, err := srv.List(ctx, &publicv1.ComputeInstancesListRequest{
			Offset: proto.Int32(1),
			Limit:  proto.Int32(1),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(listResp.GetItems()).To(HaveLen(1))
		Expect(listResp.GetItems()[0].GetId()).To(Equal("b"))
		Expect(listResp.GetSize()).To(Equal(int32(1)))
		Expect(listResp.GetTotal()).To(Equal(int32(3)))
	})

	// TC-U-125: Delete removes a known id; a second Delete is NotFound
	It("deletes a known id and returns NotFound on a second Delete (TC-U-125)", func() {
		_, err := srv.Create(ctx, &publicv1.ComputeInstancesCreateRequest{Object: &publicv1.ComputeInstance{Id: "x"}})
		Expect(err).NotTo(HaveOccurred())

		_, err = srv.Delete(ctx, &publicv1.ComputeInstancesDeleteRequest{Id: "x"})
		Expect(err).NotTo(HaveOccurred())

		listResp, err := srv.List(ctx, &publicv1.ComputeInstancesListRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(listResp.GetItems()).To(BeEmpty())

		_, err = srv.Delete(ctx, &publicv1.ComputeInstancesDeleteRequest{Id: "x"})
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.NotFound))
	})
})
