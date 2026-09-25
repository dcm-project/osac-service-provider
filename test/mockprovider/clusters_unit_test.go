package mockprovider_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/private/v1"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
	"github.com/dcm-project/osac-service-provider/test/mockprovider"
)

var _ = Describe("ClustersServer", func() {
	var (
		ctx context.Context
		srv *mockprovider.ClustersServer
	)

	BeforeEach(func() {
		ctx = context.Background()
		srv = mockprovider.NewClustersServer()
	})

	// TC-U-114: Create rejects an empty id
	It("rejects Create with an empty id (TC-U-114)", func() {
		_, err := srv.Create(ctx, &publicv1.ClustersCreateRequest{
			Object: &publicv1.Cluster{Id: ""},
		})
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.InvalidArgument))

		listResp, err := srv.List(ctx, &publicv1.ClustersListRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(listResp.GetItems()).To(BeEmpty())
	})

	// TC-U-116: Create rejects a duplicate id
	It("rejects Create with a duplicate id, preserving the original (TC-U-116)", func() {
		_, err := srv.Create(ctx, &publicv1.ClustersCreateRequest{
			Object: &publicv1.Cluster{Id: "x", Spec: &publicv1.ClusterSpec{Template: &publicv1.ClusterTemplateReference{Id: "first"}}},
		})
		Expect(err).NotTo(HaveOccurred())

		_, err = srv.Create(ctx, &publicv1.ClustersCreateRequest{
			Object: &publicv1.Cluster{Id: "x", Spec: &publicv1.ClusterSpec{Template: &publicv1.ClusterTemplateReference{Id: "second"}}},
		})
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.AlreadyExists))

		listResp, err := srv.List(ctx, &publicv1.ClustersListRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(listResp.GetItems()).To(HaveLen(1))
		Expect(listResp.GetItems()[0].GetSpec().GetTemplate().GetId()).To(Equal("first"))
	})

	// TC-U-118: Create sets CLUSTER_STATE_READY and round-trips via Get/List
	It("sets CLUSTER_STATE_READY on Create and round-trips via Get/List (TC-U-118)", func() {
		createResp, err := srv.Create(ctx, &publicv1.ClustersCreateRequest{
			Object: &publicv1.Cluster{Id: "x"},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(createResp.GetObject().GetStatus().GetState()).To(Equal(publicv1.ClusterState_CLUSTER_STATE_READY))
		Expect(createResp.GetObject().GetStatus().GetKubeconfigSecret().GetId()).To(Equal("mock-kubeconfig-x"))

		getResp, err := srv.Get(ctx, &publicv1.ClustersGetRequest{Id: "x"})
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(getResp.GetObject(), createResp.GetObject())).To(BeTrue())

		listResp, err := srv.List(ctx, &publicv1.ClustersListRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(listResp.GetItems()).To(HaveLen(1))
		Expect(proto.Equal(listResp.GetItems()[0], createResp.GetObject())).To(BeTrue())
	})

	// TC-U-120: Get of an unknown id is NotFound
	It("returns NotFound for Get of an unknown id (TC-U-120)", func() {
		_, err := srv.Get(ctx, &publicv1.ClustersGetRequest{Id: "missing"})
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.NotFound))
	})

	// TC-U-122: List honors offset/limit in creation order
	It("honors offset/limit in creation order on List (TC-U-122)", func() {
		for _, id := range []string{"a", "b", "c"} {
			_, err := srv.Create(ctx, &publicv1.ClustersCreateRequest{Object: &publicv1.Cluster{Id: id}})
			Expect(err).NotTo(HaveOccurred())
		}

		listResp, err := srv.List(ctx, &publicv1.ClustersListRequest{
			Offset: proto.Int32(1),
			Limit:  proto.Int32(1),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(listResp.GetItems()).To(HaveLen(1))
		Expect(listResp.GetItems()[0].GetId()).To(Equal("b"))
		Expect(listResp.GetSize()).To(Equal(int32(1)))
		Expect(listResp.GetTotal()).To(Equal(int32(3)))

		// A negative offset clamps to 0 (all three items, in order).
		negResp, err := srv.List(ctx, &publicv1.ClustersListRequest{Offset: proto.Int32(-5)})
		Expect(err).NotTo(HaveOccurred())
		Expect(negResp.GetItems()).To(HaveLen(3))
		Expect(negResp.GetItems()[0].GetId()).To(Equal("a"))

		// An offset beyond the collection size clamps to empty, not an error.
		overResp, err := srv.List(ctx, &publicv1.ClustersListRequest{Offset: proto.Int32(100)})
		Expect(err).NotTo(HaveOccurred())
		Expect(overResp.GetItems()).To(BeEmpty())
		Expect(overResp.GetTotal()).To(Equal(int32(3)))
	})

	// TC-U-124: Delete removes a known id; a second Delete is NotFound
	It("deletes a known id and returns NotFound on a second Delete (TC-U-124)", func() {
		_, err := srv.Create(ctx, &publicv1.ClustersCreateRequest{Object: &publicv1.Cluster{Id: "x"}})
		Expect(err).NotTo(HaveOccurred())

		_, err = srv.Delete(ctx, &publicv1.ClustersDeleteRequest{Id: "x"})
		Expect(err).NotTo(HaveOccurred())

		listResp, err := srv.List(ctx, &publicv1.ClustersListRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(listResp.GetItems()).To(BeEmpty())

		_, err = srv.Delete(ctx, &publicv1.ClustersDeleteRequest{Id: "x"})
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.NotFound))
	})

	// TC-U-155: an ACTIVE Cluster's Secret reference resolves to raw kubeconfig
	// bytes; an unknown Secret ID is NotFound.
	It("resolves a Cluster's kubeconfig Secret and returns NotFound for an unknown Secret ID (TC-U-155)", func() {
		_, err := srv.Create(ctx, &publicv1.ClustersCreateRequest{Object: &publicv1.Cluster{Id: "x"}})
		Expect(err).NotTo(HaveOccurred())

		clusterResp, err := srv.Get(ctx, &publicv1.ClustersGetRequest{Id: "x"})
		Expect(err).NotTo(HaveOccurred())
		secretID := clusterResp.GetObject().GetStatus().GetKubeconfigSecret().GetId()
		Expect(secretID).To(Equal("mock-kubeconfig-x"))

		secrets := mockprovider.NewSecretsServer(srv)
		secretResp, err := secrets.Get(ctx, &privatev1.SecretsGetRequest{Id: secretID})
		Expect(err).NotTo(HaveOccurred())
		Expect(secretResp.GetObject().GetType()).To(Equal(privatev1.SecretType_SECRET_TYPE_KUBECONFIG))
		Expect(secretResp.GetObject().GetData()["kubeconfig"]).To(Equal([]byte("apiVersion: v1\nkind: Config\nclusters:\n- name: x\n  cluster:\n    server: https://mock-provider.invalid:6443\ncurrent-context: x\n")))

		_, err = secrets.Get(ctx, &privatev1.SecretsGetRequest{Id: "mock-kubeconfig-missing"})
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.NotFound))
	})
})
