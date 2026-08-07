package vm_test

import (
	"encoding/json"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
	vmservice "github.com/dcm-project/osac-service-provider/internal/vm"
)

var _ = Describe("Default Network Provisioning, Status, and Error mapping (integration, cross-cutting)", func() {
	// TC-I-340 (REQ-VMNET-020/030/040, AC-VMNET-020/030): a new default
	// network is provisioned end-to-end over real HTTP on the very first
	// VM Create.
	It("provisions a new default network end-to-end over real HTTP on the first Create (TC-I-340)", func() {
		f := newIntegrationFixture()
		defer f.Close()

		f.subnets.listFunc = func(*publicv1.SubnetsListRequest) (*publicv1.SubnetsListResponse, error) {
			return &publicv1.SubnetsListResponse{}, nil
		}

		resp := postCreate(f, validCreateJSON)
		defer func() { _ = resp.Body.Close() }()

		Expect(resp.StatusCode).To(Equal(http.StatusCreated))
		attachments := f.fake.LastCreateCall().GetObject().GetSpec().GetNetworkAttachments()
		Expect(attachments).To(HaveLen(1))
		Expect(attachments[0].GetSubnet().GetId()).To(Equal("subnet-new"))
	})

	// TC-I-341 (REQ-VMNET-040, AC-VMNET-040): network provisioning timeout
	// is surfaced as 502 over real HTTP, and ComputeInstances/Create is
	// never called.
	It("surfaces a network provisioning timeout as 502 over real HTTP (TC-I-341)", func() {
		f := newIntegrationFixture(
			vmservice.WithNetworkPollInterval(10*time.Millisecond),
			vmservice.WithNetworkPollTimeout(50*time.Millisecond),
		)
		defer f.Close()

		f.subnets.listFunc = func(*publicv1.SubnetsListRequest) (*publicv1.SubnetsListResponse, error) {
			return &publicv1.SubnetsListResponse{}, nil
		}
		f.subnets.getFunc = func(req *publicv1.SubnetsGetRequest) (*publicv1.SubnetsGetResponse, error) {
			return &publicv1.SubnetsGetResponse{Object: &publicv1.Subnet{
				Id:     req.GetId(),
				Status: &publicv1.SubnetStatus{State: publicv1.SubnetState_SUBNET_STATE_PENDING},
			}}, nil
		}

		resp := postCreate(f, validCreateJSON)
		defer func() { _ = resp.Body.Close() }()

		Expect(resp.StatusCode).To(Equal(http.StatusBadGateway))
		Expect(f.fake.CreateCallCount()).To(Equal(0))
	})

	// TC-I-342 (REQ-VMNET-010, AC-VMNET-010): an existing default subnet
	// is reused over real HTTP — proven by the *absence* of any
	// VirtualNetworks/Subnets Create call, not merely by which subnet id
	// ends up attached (that positive assertion alone is already made
	// incidentally by every other Create test using this fixture's
	// default "subnet-existing" behavior — TC-I-303, for instance — but
	// none of them assert the negative "no new network" half of the AC
	// until now).
	It("reuses an existing default subnet without creating a new network, over real HTTP (TC-I-342)", func() {
		f := newIntegrationFixture()
		defer f.Close()

		resp := postCreate(f, validCreateJSON)
		defer func() { _ = resp.Body.Close() }()

		Expect(resp.StatusCode).To(Equal(http.StatusCreated))
		Expect(f.vnets.CreateCallCount()).To(Equal(0))
		Expect(f.subnets.CreateCallCount()).To(Equal(0))
		attachments := f.fake.LastCreateCall().GetObject().GetSpec().GetNetworkAttachments()
		Expect(attachments).To(HaveLen(1))
		Expect(attachments[0].GetSubnet().GetId()).To(Equal("subnet-existing"))
	})

	// TC-I-350 (REQ-VMSTATUS-020, AC-VMSTATUS-020): status precedence —
	// a connectivity failure and a real 404 are never conflated, over
	// real HTTP.
	It("distinguishes a connectivity failure from a real NotFound, over real HTTP (TC-I-350)", func() {
		f := newIntegrationFixture()
		defer f.Close()

		f.fake.getFunc = func(*publicv1.ComputeInstancesGetRequest) (*publicv1.ComputeInstancesGetResponse, error) {
			return nil, grpcstatus.Error(codes.Unavailable, "osac unreachable")
		}
		unavailableResp := getVM(f)
		defer func() { _ = unavailableResp.Body.Close() }()
		Expect(unavailableResp.StatusCode).To(Equal(http.StatusBadGateway))

		f.fake.getFunc = func(*publicv1.ComputeInstancesGetRequest) (*publicv1.ComputeInstancesGetResponse, error) {
			return nil, grpcstatus.Error(codes.NotFound, "no such compute instance")
		}
		notFoundResp := getVM(f)
		defer func() { _ = notFoundResp.Body.Close() }()
		Expect(notFoundResp.StatusCode).To(Equal(http.StatusNotFound))
	})

	// TC-I-360 (REQ-VMERR-010/020/030, AC-VMERR-010/020): each gRPC error
	// code maps to its documented HTTP status over real HTTP, identically
	// across handlers.
	DescribeTable("maps each gRPC code to its documented HTTP status over real HTTP (TC-I-360)",
		func(code codes.Code, wantStatus int, wantType v1alpha1.ErrorType) {
			f := newIntegrationFixture()
			defer f.Close()

			f.fake.getFunc = func(*publicv1.ComputeInstancesGetRequest) (*publicv1.ComputeInstancesGetResponse, error) {
				return nil, grpcstatus.Error(code, "boom")
			}

			resp := getVM(f)
			defer func() { _ = resp.Body.Close() }()

			Expect(resp.StatusCode).To(Equal(wantStatus))
			var body v1alpha1.Error
			Expect(json.NewDecoder(resp.Body).Decode(&body)).To(Succeed())
			Expect(body.Type).To(Equal(wantType))
		},
		Entry("InvalidArgument -> 400", codes.InvalidArgument, http.StatusBadRequest, v1alpha1.INVALIDARGUMENT),
		Entry("Unauthenticated -> 401", codes.Unauthenticated, http.StatusUnauthorized, v1alpha1.UNAUTHENTICATED),
		Entry("PermissionDenied -> 403", codes.PermissionDenied, http.StatusForbidden, v1alpha1.PERMISSIONDENIED),
		Entry("NotFound -> 404", codes.NotFound, http.StatusNotFound, v1alpha1.NOTFOUND),
		Entry("Unavailable -> 502", codes.Unavailable, http.StatusBadGateway, v1alpha1.UNAVAILABLE),
		Entry("Internal -> 500", codes.Internal, http.StatusInternalServerError, v1alpha1.INTERNAL),
	)

	It("produces an identical PermissionDenied mapping across List and Delete, over real HTTP (TC-I-360b)", func() {
		f := newIntegrationFixture()
		defer f.Close()

		f.fake.listFunc = func(*publicv1.ComputeInstancesListRequest) (*publicv1.ComputeInstancesListResponse, error) {
			return nil, grpcstatus.Error(codes.PermissionDenied, "denied")
		}
		f.fake.deleteFunc = func(*publicv1.ComputeInstancesDeleteRequest) (*publicv1.ComputeInstancesDeleteResponse, error) {
			return nil, grpcstatus.Error(codes.PermissionDenied, "denied")
		}

		listResp := listVMs(f, "")
		defer func() { _ = listResp.Body.Close() }()
		deleteResp := deleteVM(f)
		defer func() { _ = deleteResp.Body.Close() }()

		Expect(listResp.StatusCode).To(Equal(http.StatusForbidden))
		Expect(deleteResp.StatusCode).To(Equal(http.StatusForbidden))

		var listErr, deleteErr v1alpha1.Error
		Expect(json.NewDecoder(listResp.Body).Decode(&listErr)).To(Succeed())
		Expect(json.NewDecoder(deleteResp.Body).Decode(&deleteErr)).To(Succeed())
		Expect(listErr.Type).To(Equal(v1alpha1.PERMISSIONDENIED))
		Expect(deleteErr.Type).To(Equal(v1alpha1.PERMISSIONDENIED))
	})
})
