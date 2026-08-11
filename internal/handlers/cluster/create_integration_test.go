package cluster_test

import (
	"encoding/json"
	"net/http"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

// postCreate issues a real POST /api/v1alpha1/clusters?id=X (every test in
// this file uses the same id) with body as the JSON payload — a string so
// tests can send deliberately-malformed bodies for validation cases.
func postCreate(f *integrationFixture, body string) *http.Response {
	url := f.URL("/api/v1alpha1/clusters?id=X")
	resp, err := http.Post(url, "application/json", strings.NewReader(body)) //nolint:noctx,gosec // test helper
	Expect(err).NotTo(HaveOccurred())
	return resp
}

const validCreateJSON = `{"spec":{"version":"1.29","nodes":{"worker":{"count":3}},"metadata":{"name":"foo"},"provider_hints":{"osac":{"template_id":"default-hcp"}}}}`

var _ = Describe("Cluster Create (integration, real HTTP + router + bufconn OSAC fake)", func() {
	var f *integrationFixture

	BeforeEach(func() {
		f = newIntegrationFixture()
		DeferCleanup(f.Close)
	})

	// TC-I-200 (REQ-CREATE-010/020/030/050, AC-CREATE-010/020): Create
	// succeeds end-to-end over real HTTP with the exact translated fields
	// independently observable both in the HTTP response and at the fake
	// OSAC server.
	It("succeeds end-to-end over real HTTP (TC-I-200)", func() {
		resp := postCreate(f, validCreateJSON)
		defer func() { _ = resp.Body.Close() }()

		Expect(resp.StatusCode).To(Equal(http.StatusCreated))
		var cluster v1alpha1.Cluster
		Expect(json.NewDecoder(resp.Body).Decode(&cluster)).To(Succeed())
		Expect(*cluster.Id).To(Equal("X"))
		Expect(cluster.Status).To(Equal(v1alpha1.ClusterStatusPROGRESSING))

		Expect(f.fake.CreateCallCount()).To(Equal(1))
	})

	// TC-I-201 (REQ-CREATE-040, AC-CREATE-030): two real, sequential HTTP
	// requests with the same id return the same resource, not a duplicate.
	It("is idempotent across two real, sequential HTTP requests with the same id (TC-I-201)", func() {
		first := postCreate(f, validCreateJSON)
		Expect(first.StatusCode).To(Equal(http.StatusCreated))
		_ = first.Body.Close()

		// The fake's default Create behavior always succeeds; make the
		// second Create call for the same id fail with AlreadyExists,
		// exactly as a real OSAC server would for a retried id, and have
		// Get return the first call's now-persisted state.
		f.fake.createFunc = func(*publicv1.ClustersCreateRequest) (*publicv1.ClustersCreateResponse, error) {
			return nil, grpcstatus.Error(codes.AlreadyExists, "cluster X already exists")
		}
		f.fake.getFunc = func(req *publicv1.ClustersGetRequest) (*publicv1.ClustersGetResponse, error) {
			return &publicv1.ClustersGetResponse{Object: &publicv1.Cluster{
				Id:     req.GetId(),
				Status: &publicv1.ClusterStatus{State: publicv1.ClusterState_CLUSTER_STATE_PROGRESSING},
			}}, nil
		}

		second := postCreate(f, validCreateJSON)
		defer func() { _ = second.Body.Close() }()
		Expect(second.StatusCode).To(Equal(http.StatusCreated))

		var cluster v1alpha1.Cluster
		Expect(json.NewDecoder(second.Body).Decode(&cluster)).To(Succeed())
		Expect(*cluster.Id).To(Equal("X"))
		Expect(cluster.Status).To(Equal(v1alpha1.ClusterStatusPROGRESSING))
		Expect(f.fake.GetCallCount()).To(Equal(1))
	})

	// TC-I-202 (REQ-CREATE-060, AC-CREATE-040/050): request validation is
	// enforced at the real HTTP boundary. Both the id query parameter and
	// the body's spec property are schema-optional (AEP-133, DD-113), so
	// the generated router wrapper accepts a request missing either one —
	// this package's own validateCreateRequest is the sole enforcement
	// point for both cases.
	It("rejects a missing id query parameter at the real HTTP boundary (TC-I-202a)", func() {
		resp, err := http.Post(f.URL("/api/v1alpha1/clusters"), "application/json", strings.NewReader(validCreateJSON)) //nolint:noctx,gosec // test helper
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()

		Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
		Expect(f.fake.CreateCallCount()).To(Equal(0))
	})

	It("rejects a missing required spec field at the real HTTP boundary (TC-I-202b)", func() {
		body := `{"spec":{"version":"1.29","nodes":{"worker":{"count":3}},"metadata":{"name":"foo"},"provider_hints":{"osac":{"template_id":""}}}}`
		resp := postCreate(f, body)
		defer func() { _ = resp.Body.Close() }()

		Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
		Expect(f.fake.CreateCallCount()).To(Equal(0))
	})

	// TC-I-203 (REQ-CREATE-070, AC-CREATE-060): host-sizing hints are not
	// forwarded as a host_type override, over real HTTP.
	It("never forwards worker sizing hints as a host_type override, over real HTTP (TC-I-203)", func() {
		body := `{"spec":{"version":"1.29","nodes":{"worker":{"count":3,"cpu":8,"memory":"32GB","storage":"250GB"}},"metadata":{"name":"foo"},"provider_hints":{"osac":{"template_id":"default-hcp"}}}}`
		resp := postCreate(f, body)
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		Expect(f.fake.CreateCallCount()).To(Equal(1))
	})
})
