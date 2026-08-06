package statuspoll_test

// Integration scope (per
// .ai/test-plans/osac-sp-m5-status-reporting.test-plan.md, section 6): a
// real, unstubbed collaborator crossing a protocol boundary this SP does
// not control — a real bufconn-backed gRPC server (mirroring Milestone 2's
// SC-M2-003 technique), since this milestone has no REST/HTTP surface of
// its own to stand in for that role.
//
// This file is package statuspoll_test (external/black-box, unlike
// poller_unit_test.go's white-box package statuspoll) since it only needs
// the public API — but it shares poller_test.go's single
// TestStatusPoll/RunSpecs entry point (same pattern as
// internal/statuspublisher's publisher_integration_test.go).

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/dcm-project/osac-service-provider/internal/config"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
	"github.com/dcm-project/osac-service-provider/internal/statuspoll"
	"github.com/dcm-project/osac-service-provider/internal/statuspublisher"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// itFakeClustersServer is a real, wire-level fake implementing the
// generated ClustersServer interface, serving one canned page of results.
type itFakeClustersServer struct {
	publicv1.UnimplementedClustersServer
	resp *publicv1.ClustersListResponse
}

func (s *itFakeClustersServer) List(context.Context, *publicv1.ClustersListRequest) (*publicv1.ClustersListResponse, error) {
	return s.resp, nil
}

type itFakeComputeInstancesServer struct {
	publicv1.UnimplementedComputeInstancesServer
	resp *publicv1.ComputeInstancesListResponse
}

func (s *itFakeComputeInstancesServer) List(context.Context, *publicv1.ComputeInstancesListRequest) (*publicv1.ComputeInstancesListResponse, error) {
	return s.resp, nil
}

// itPublishCall/itFakePublisher mirror poller_unit_test.go's fakePublisher,
// duplicated here since this file's package (statuspoll_test) cannot see
// that file's unexported types.
type itPublishCall struct {
	subject    string
	resourceID string
	status     string
	message    string
}

type itFakePublisher struct {
	mu    sync.Mutex
	calls []itPublishCall
}

func (f *itFakePublisher) Publish(st statuspublisher.ServiceType, resourceID, status, message string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, itPublishCall{subject: st.Subject, resourceID: resourceID, status: status, message: message})
}

func (f *itFakePublisher) Calls() []itPublishCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]itPublishCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// dialBufconn starts a real, in-process gRPC server bound to a
// bufconn.Listener hosting the Clusters/ComputeInstances fakes, and
// returns a *grpc.ClientConn dialed against it.
func dialBufconn(clusters publicv1.ClustersServer, computeInstances publicv1.ComputeInstancesServer) (*grpc.ClientConn, func()) {
	lis := bufconn.Listen(1024 * 1024)
	grpcSrv := grpc.NewServer()
	publicv1.RegisterClustersServer(grpcSrv, clusters)
	publicv1.RegisterComputeInstancesServer(grpcSrv, computeInstances)
	go func() { _ = grpcSrv.Serve(lis) }()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
	)
	Expect(err).NotTo(HaveOccurred())

	return conn, func() {
		_ = conn.Close()
		grpcSrv.Stop()
	}
}

var _ = Describe("End-to-end poll wiring over real gRPC", func() {
	// TC-I-450: a full poll cycle reaches through real gRPC serialization
	// to a real fake Publisher.
	It("maps a real gRPC List response through MapStatus and message derivation to Publish (TC-I-450)", func() {
		clustersSrv := &itFakeClustersServer{resp: &publicv1.ClustersListResponse{
			Size: 1, Total: 1,
			Items: []*publicv1.Cluster{{
				Id: "cluster-1",
				Status: &publicv1.ClusterStatus{
					State: publicv1.ClusterState_CLUSTER_STATE_READY,
					Conditions: []*publicv1.ClusterCondition{
						{
							Type:    publicv1.ClusterConditionType_CLUSTER_CONDITION_TYPE_READY,
							Status:  publicv1.ConditionStatus_CONDITION_STATUS_TRUE,
							Message: strPtr("control plane healthy"),
						},
					},
				},
			}},
		}}
		computeInstancesSrv := &itFakeComputeInstancesServer{resp: &publicv1.ComputeInstancesListResponse{
			Size: 1, Total: 1,
			Items: []*publicv1.ComputeInstance{{
				Id:     "vm-1",
				Status: &publicv1.ComputeInstanceStatus{State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING},
			}},
		}}

		conn, closeConn := dialBufconn(clustersSrv, computeInstancesSrv)
		defer closeConn()

		clustersClient := publicv1.NewClustersClient(conn)
		computeInstancesClient := publicv1.NewComputeInstancesClient(conn)
		publisher := &itFakePublisher{}

		poller := statuspoll.New(clustersClient, computeInstancesClient, publisher,
			config.StatusConfig{PollInterval: time.Hour, ResyncEvery: 100}, slog.New(slog.DiscardHandler))

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		poller.Start(ctx)

		Eventually(publisher.Calls, 5*time.Second).Should(HaveLen(2))

		byID := map[string]itPublishCall{}
		for _, c := range publisher.Calls() {
			byID[c.resourceID] = c
		}

		cluster := byID["cluster-1"]
		Expect(cluster.status).To(Equal("ACTIVE"))
		Expect(cluster.message).To(Equal("control plane healthy"))

		vm := byID["vm-1"]
		Expect(vm.status).To(Equal("RUNNING"))
		Expect(vm.message).To(Equal("vm is running"))
	})
})

func strPtr(s string) *string { return &s }
