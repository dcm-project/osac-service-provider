package statuspoll

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc"

	"github.com/dcm-project/osac-service-provider/internal/config"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
	"github.com/dcm-project/osac-service-provider/internal/statuspublisher"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeClustersClient is a hand-written fake satisfying ClustersClient
// (CLAUDE.md: no mocking framework). Its responder is swappable between
// runCycle calls so a test can drive multi-cycle scenarios by mutating
// fake state directly, without any internal cycle-tracking of its own.
type fakeClustersClient struct {
	mu                  sync.Mutex
	calls               []*publicv1.ClustersListRequest
	responder           func(offset int32) (*publicv1.ClustersListResponse, error)
	blockUntilCancelled bool
}

func (f *fakeClustersClient) List(ctx context.Context, req *publicv1.ClustersListRequest, _ ...grpc.CallOption) (*publicv1.ClustersListResponse, error) {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	responder := f.responder
	blockUntilCancelled := f.blockUntilCancelled
	f.mu.Unlock()
	if blockUntilCancelled {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if responder == nil {
		return &publicv1.ClustersListResponse{}, nil
	}
	return responder(req.GetOffset())
}

func (f *fakeClustersClient) Calls() []*publicv1.ClustersListRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*publicv1.ClustersListRequest, len(f.calls))
	copy(out, f.calls)
	return out
}

// SetItems serves every item on a single page, regardless of offset/limit.
func (f *fakeClustersClient) SetItems(items []*publicv1.Cluster) {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := int32(len(items))
	f.responder = func(int32) (*publicv1.ClustersListResponse, error) {
		return &publicv1.ClustersListResponse{Items: items, Size: total, Total: total}, nil
	}
}

// SetPages serves items paginated pageSize at a time, honoring offset.
func (f *fakeClustersClient) SetPages(pageSize int32, items []*publicv1.Cluster) {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := int32(len(items))
	f.responder = func(offset int32) (*publicv1.ClustersListResponse, error) {
		if offset > total {
			offset = total
		}
		end := offset + pageSize
		if end > total {
			end = total
		}
		page := items[offset:end]
		return &publicv1.ClustersListResponse{Items: page, Size: int32(len(page)), Total: total}, nil
	}
}

func (f *fakeClustersClient) SetError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responder = func(int32) (*publicv1.ClustersListResponse, error) {
		return nil, err
	}
}

// SetInconsistentEmptyPage serves a single page reporting Size=0 while
// Total remains positive — a buggy/inconsistent server response, used to
// regression-test that pagination terminates rather than looping forever
// trusting the Size field for offset advancement (TC-U-466).
func (f *fakeClustersClient) SetInconsistentEmptyPage(total int32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responder = func(int32) (*publicv1.ClustersListResponse, error) {
		return &publicv1.ClustersListResponse{Items: nil, Size: 0, Total: total}, nil
	}
}

// SetBlockingUntilCancelled makes List block until its context is
// cancelled/expires, then return ctx.Err() — simulating a hung backend
// bounded only by the caller's own per-call timeout (TC-U-465).
func (f *fakeClustersClient) SetBlockingUntilCancelled() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blockUntilCancelled = true
}

// fakeComputeInstancesClient mirrors fakeClustersClient for ComputeInstances.
type fakeComputeInstancesClient struct {
	mu        sync.Mutex
	calls     []*publicv1.ComputeInstancesListRequest
	responder func(offset int32) (*publicv1.ComputeInstancesListResponse, error)
}

func (f *fakeComputeInstancesClient) List(_ context.Context, req *publicv1.ComputeInstancesListRequest, _ ...grpc.CallOption) (*publicv1.ComputeInstancesListResponse, error) {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	responder := f.responder
	f.mu.Unlock()
	if responder == nil {
		return &publicv1.ComputeInstancesListResponse{}, nil
	}
	return responder(req.GetOffset())
}

func (f *fakeComputeInstancesClient) Calls() []*publicv1.ComputeInstancesListRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*publicv1.ComputeInstancesListRequest, len(f.calls))
	copy(out, f.calls)
	return out
}

func (f *fakeComputeInstancesClient) SetItems(items []*publicv1.ComputeInstance) {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := int32(len(items))
	f.responder = func(int32) (*publicv1.ComputeInstancesListResponse, error) {
		return &publicv1.ComputeInstancesListResponse{Items: items, Size: total, Total: total}, nil
	}
}

func (f *fakeComputeInstancesClient) SetPages(pageSize int32, items []*publicv1.ComputeInstance) {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := int32(len(items))
	f.responder = func(offset int32) (*publicv1.ComputeInstancesListResponse, error) {
		if offset > total {
			offset = total
		}
		end := offset + pageSize
		if end > total {
			end = total
		}
		page := items[offset:end]
		return &publicv1.ComputeInstancesListResponse{Items: page, Size: int32(len(page)), Total: total}, nil
	}
}

func (f *fakeComputeInstancesClient) SetError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responder = func(int32) (*publicv1.ComputeInstancesListResponse, error) {
		return nil, err
	}
}

// publishCall records one Publish invocation observed by fakePublisher.
type publishCall struct {
	st         statuspublisher.ServiceType
	resourceID string
	status     string
	message    string
}

// fakePublisher is a hand-written fake satisfying Publisher.
type fakePublisher struct {
	mu    sync.Mutex
	calls []publishCall
}

func (f *fakePublisher) Publish(st statuspublisher.ServiceType, resourceID, status, message string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, publishCall{st: st, resourceID: resourceID, status: status, message: message})
}

func (f *fakePublisher) Calls() []publishCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]publishCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func (f *fakePublisher) Len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// CallsSince returns the calls recorded after the first n calls — used to
// isolate a single cycle's delta contribution across a multi-cycle test.
func (f *fakePublisher) CallsSince(n int) []publishCall {
	all := f.Calls()
	if n >= len(all) {
		return nil
	}
	return all[n:]
}

func (f *fakePublisher) CallsFor(resourceID string) []publishCall {
	var out []publishCall
	for _, c := range f.Calls() {
		if c.resourceID == resourceID {
			out = append(out, c)
		}
	}
	return out
}

var discardLogger = slog.New(slog.DiscardHandler)

func readyCluster(id string) *publicv1.Cluster {
	return &publicv1.Cluster{Id: id, Status: &publicv1.ClusterStatus{State: publicv1.ClusterState_CLUSTER_STATE_READY}}
}

func runningVM(id string) *publicv1.ComputeInstance {
	return &publicv1.ComputeInstance{Id: id, Status: &publicv1.ComputeInstanceStatus{State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING}}
}

var _ = Describe("Poll cycle: listing, diffing, resync", func() {
	var (
		clusters         *fakeClustersClient
		computeInstances *fakeComputeInstancesClient
		publisher        *fakePublisher
		ctx              context.Context
	)

	BeforeEach(func() {
		clusters = &fakeClustersClient{}
		computeInstances = &fakeComputeInstancesClient{}
		publisher = &fakePublisher{}
		ctx = context.Background()
	})

	newPoller := func(resyncEvery int) *Poller {
		return New(clusters, computeInstances, publisher, "osac-sp-cluster", "osac-sp-vm", config.StatusConfig{PollInterval: time.Hour, ResyncEvery: resyncEvery}, discardLogger)
	}

	// TC-U-450 (REQ-POLL-020, AC-POLL-010): pages through all results
	// using the ownership filter.
	It("pages through all results using the ownership filter (TC-U-450)", func() {
		clusterItems := make([]*publicv1.Cluster, 5)
		vmItems := make([]*publicv1.ComputeInstance, 5)
		for i := range clusterItems {
			clusterItems[i] = readyCluster(fmt.Sprintf("c-%d", i))
			vmItems[i] = runningVM(fmt.Sprintf("vm-%d", i))
		}
		clusters.SetPages(2, clusterItems)
		computeInstances.SetPages(2, vmItems)

		p := newPoller(100)
		p.runCycle(ctx)

		Expect(clusters.Calls()).To(HaveLen(3))
		Expect(computeInstances.Calls()).To(HaveLen(3))
		for _, c := range clusters.Calls() {
			Expect(c.GetFilter()).To(Equal(ownershipFilter))
		}
		for _, c := range computeInstances.Calls() {
			Expect(c.GetFilter()).To(Equal(ownershipFilter))
		}

		var clusterIDs, vmIDs []string
		for _, call := range publisher.Calls() {
			switch call.st.Subject {
			case clusterSubject:
				clusterIDs = append(clusterIDs, call.resourceID)
			case vmSubject:
				vmIDs = append(vmIDs, call.resourceID)
			}
		}
		Expect(clusterIDs).To(ConsistOf("c-0", "c-1", "c-2", "c-3", "c-4"))
		Expect(vmIDs).To(ConsistOf("vm-0", "vm-1", "vm-2", "vm-3", "vm-4"))
	})

	// TC-U-466 (REQ-POLL-020, AC-POLL-010, regression): pagination
	// terminates outright once a page returns zero items, even if Total
	// claims more remain — guards against a Size=0/Total>0 response
	// stalling offset (previously advanced by the server-reported Size
	// field, not len(items)) and looping forever.
	It("terminates pagination when a page reports Size=0 while Total>0 (TC-U-466)", func() {
		clusters.SetInconsistentEmptyPage(5)

		p := newPoller(100)
		p.runCycle(ctx)

		Expect(clusters.Calls()).To(HaveLen(1), "the page loop must terminate on the first empty page rather than looping forever trusting Size")
	})

	// TC-U-464 (REQ-POLL-015, AC-POLL-015, regression): Source is built
	// from the caller-supplied provider names, not a hardcoded literal
	// independent of config.
	It("builds Source from the caller-supplied provider names (TC-U-464)", func() {
		p := New(clusters, computeInstances, publisher, "custom-cluster", "custom-vm",
			config.StatusConfig{PollInterval: time.Hour, ResyncEvery: 100}, discardLogger)
		clusters.SetItems([]*publicv1.Cluster{readyCluster("c-1")})
		computeInstances.SetItems([]*publicv1.ComputeInstance{runningVM("vm-1")})

		p.runCycle(ctx)

		clusterCalls := publisher.CallsFor("c-1")
		Expect(clusterCalls).To(HaveLen(1))
		Expect(clusterCalls[0].st.Source).To(Equal("dcm/providers/custom-cluster"))

		vmCalls := publisher.CallsFor("vm-1")
		Expect(vmCalls).To(HaveLen(1))
		Expect(vmCalls[0].st.Source).To(Equal("dcm/providers/custom-vm"))
	})

	// TC-U-451 (REQ-POLL-040, AC-POLL-020): a changed status is published
	// immediately; an unchanged one is not.
	It("publishes a changed status immediately and skips an unchanged one (TC-U-451)", func() {
		p := newPoller(100)
		clusters.SetItems([]*publicv1.Cluster{{Id: "c-1", Status: &publicv1.ClusterStatus{State: publicv1.ClusterState_CLUSTER_STATE_PROGRESSING}}})
		p.runCycle(ctx) // seeds the cache with PROGRESSING (AC-POLL-020's "Given")

		before := publisher.Len()
		clusters.SetItems([]*publicv1.Cluster{readyCluster("c-1")})
		p.runCycle(ctx) // status now READY -> ACTIVE
		changed := publisher.CallsSince(before)
		Expect(changed).To(HaveLen(1))
		Expect(changed[0].resourceID).To(Equal("c-1"))
		Expect(changed[0].status).To(Equal("ACTIVE"))

		before2 := publisher.Len()
		p.runCycle(ctx) // unchanged
		Expect(publisher.CallsSince(before2)).To(BeEmpty())
	})

	// TC-U-452 (REQ-POLL-040): a newly observed resource is published on
	// first sight.
	It("publishes a newly observed resource on first sight (TC-U-452)", func() {
		p := newPoller(100)
		p.runCycle(ctx) // cycle 1: nothing

		before := publisher.Len()
		computeInstances.SetItems([]*publicv1.ComputeInstance{runningVM("vm-1")})
		p.runCycle(ctx) // cycle 2: new VM

		calls := publisher.CallsSince(before)
		Expect(calls).To(HaveLen(1))
		Expect(calls[0].resourceID).To(Equal("vm-1"))
		Expect(calls[0].status).To(Equal("RUNNING"))
	})

	// TC-U-453 (REQ-POLL-050, AC-POLL-030): a disappeared resource is
	// reported DELETED exactly once.
	It("reports a disappeared resource as DELETED exactly once (TC-U-453)", func() {
		p := newPoller(100)
		clusters.SetItems([]*publicv1.Cluster{readyCluster("c-1")})
		p.runCycle(ctx) // cycle 1: present

		before := publisher.Len()
		clusters.SetItems(nil)
		p.runCycle(ctx) // cycle 2: absent
		calls := publisher.CallsSince(before)
		Expect(calls).To(HaveLen(1))
		Expect(calls[0].resourceID).To(Equal("c-1"))
		Expect(calls[0].status).To(Equal("DELETED"))

		before2 := publisher.Len()
		p.runCycle(ctx) // cycle 3: still absent
		Expect(publisher.CallsSince(before2)).To(BeEmpty())
	})

	// TC-U-454 (REQ-POLL-080, AC-POLL-060): periodic full resync
	// republishes every resource regardless of cache state.
	It("periodically republishes every resource regardless of cache state (TC-U-454)", func() {
		p := newPoller(3)
		clusters.SetItems([]*publicv1.Cluster{readyCluster("c-1")})

		perCycle := make([]int, 0, 4)
		for range 4 {
			before := publisher.Len()
			p.runCycle(ctx)
			perCycle = append(perCycle, len(publisher.CallsSince(before)))
		}
		Expect(perCycle).To(Equal([]int{1, 0, 0, 1}), "resync fires on cycles 0 and 3 only, per ResyncEvery=3")
	})

	// TC-U-455 (REQ-POLL-090, AC-POLL-070): a List failure for one
	// service type does not stop the loop or block the other.
	It("does not stop the loop or the other service type on a List failure (TC-U-455)", func() {
		p := newPoller(100)
		clusters.SetError(errors.New("boom"))
		computeInstances.SetItems([]*publicv1.ComputeInstance{runningVM("vm-1")})

		p.runCycle(ctx) // cycle 1: clusters fails, VMs still processed
		vmCalls := publisher.CallsFor("vm-1")
		Expect(vmCalls).To(HaveLen(1))

		before := publisher.Len()
		clusters.SetItems([]*publicv1.Cluster{readyCluster("c-1")})
		p.runCycle(ctx) // cycle 2: both succeed normally
		calls := publisher.CallsSince(before)
		ids := make([]string, 0, len(calls))
		for _, c := range calls {
			ids = append(ids, c.resourceID)
		}
		Expect(ids).To(ContainElement("c-1"))
	})

	// TC-U-465 (REQ-POLL-025, AC-POLL-080, regression): a hung List call
	// is bounded by ListTimeout and treated as a failure — cluster
	// processing is skipped for that cycle while VM processing still
	// completes normally.
	It("bounds a hung List call by ListTimeout and skips that service type (TC-U-465)", func() {
		clusters.SetBlockingUntilCancelled()
		computeInstances.SetItems([]*publicv1.ComputeInstance{runningVM("vm-1")})

		p := New(clusters, computeInstances, publisher, "osac-sp-cluster", "osac-sp-vm",
			config.StatusConfig{PollInterval: time.Hour, ResyncEvery: 100, ListTimeout: 50 * time.Millisecond}, discardLogger)

		start := time.Now()
		p.runCycle(ctx)
		Expect(time.Since(start)).To(BeNumerically("<", time.Second), "the cycle must not block on the hung List call past its own ListTimeout")

		Expect(publisher.CallsFor("c-1")).To(BeEmpty(), "cluster processing must be skipped for this cycle")
		Expect(publisher.CallsFor("vm-1")).To(HaveLen(1), "VM processing must still complete normally")
	})

	// Symmetric case for TC-U-455/REQ-POLL-090: a ComputeInstances.List
	// failure skips only VM processing for that cycle; Cluster processing
	// (and the loop) still proceeds.
	It("does not stop the loop or block Clusters on a ComputeInstances.List failure", func() {
		p := newPoller(100)
		clusters.SetItems([]*publicv1.Cluster{readyCluster("c-1")})
		computeInstances.SetError(errors.New("boom"))

		p.runCycle(ctx)
		Expect(publisher.CallsFor("c-1")).To(HaveLen(1))
		Expect(publisher.CallsFor("vm-1")).To(BeEmpty())
	})

	// Symmetric case for TC-U-453/REQ-POLL-050: a disappeared VM is
	// reported DELETED exactly once, mirroring the Cluster case.
	It("reports a disappeared VM as DELETED exactly once", func() {
		p := newPoller(100)
		computeInstances.SetItems([]*publicv1.ComputeInstance{runningVM("vm-1")})
		p.runCycle(ctx) // present

		before := publisher.Len()
		computeInstances.SetItems(nil)
		p.runCycle(ctx) // absent
		calls := publisher.CallsSince(before)
		Expect(calls).To(HaveLen(1))
		Expect(calls[0].resourceID).To(Equal("vm-1"))
		Expect(calls[0].status).To(Equal("DELETED"))
	})

	// REQ-POLL-100: Done() closes exactly once the poll loop returns.
	It("closes Done() once the poll loop returns on ctx cancellation", func() {
		p := newPoller(100)
		ctx, cancel := context.WithCancel(context.Background())
		p.Start(ctx)
		cancel()
		Eventually(p.Done(), time.Second).Should(BeClosed())
	})

	// New treats a non-positive ResyncEvery as 1 (every cycle resyncs)
	// instead of panicking on a modulo-by-zero.
	It("treats a non-positive ResyncEvery as 1, resyncing every cycle", func() {
		p := New(clusters, computeInstances, publisher, "osac-sp-cluster", "osac-sp-vm", config.StatusConfig{PollInterval: time.Hour, ResyncEvery: 0}, discardLogger)
		clusters.SetItems([]*publicv1.Cluster{readyCluster("c-1")})

		p.runCycle(ctx)
		before := publisher.Len()
		p.runCycle(ctx) // unchanged status, but every cycle resyncs
		Expect(publisher.CallsSince(before)).To(HaveLen(1))
	})
})
