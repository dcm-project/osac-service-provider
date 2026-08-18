// Package statuspoll periodically lists every DCM-owned Cluster and VM from
// OSAC, computes each one's current (status, message), and reports any
// new/changed/disappeared resource to internal/statuspublisher.
//
// Implements REQ-POLL-* (see .ai/specs/osac-sp-m5-status-reporting.spec.md).
// This is the only Milestone 5 package that imports Milestone 3's
// internal/cluster and Milestone 4's internal/vm (for MapStatus), and
// therefore the only part of this milestone that cannot compile until
// those two milestones land on main — see DD-075.
package statuspoll

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	"github.com/dcm-project/osac-service-provider/internal/cluster"
	"github.com/dcm-project/osac-service-provider/internal/config"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
	"github.com/dcm-project/osac-service-provider/internal/statuspublisher"
	"github.com/dcm-project/osac-service-provider/internal/util"
	"github.com/dcm-project/osac-service-provider/internal/vm"
)

// ownershipFilter is the CEL filter always applied to both List calls
// (REQ-POLL-020) — identical to internal/cluster/list.go's and
// internal/vm/list.go's own ownershipFilter.
const ownershipFilter = `this.metadata.labels["dcm.io/managed-by"] == "dcm"`

// listPageSize bounds each List page while the Poller pages through every
// result (REQ-POLL-020).
const listPageSize int32 = 100

// disappearedMessage is the synthesized message used when a previously
// cached resource is absent from the current cycle's listing
// (REQ-POLL-050).
const disappearedMessage = "resource no longer found"

// defaultListTimeout bounds each individual List call (REQ-POLL-025) when
// cfg.ListTimeout is unset/non-positive, so a hung OSAC backend can never
// wedge the poll loop indefinitely — same resilience principle as this
// repo's own DD-091 (an unbounded wait must always be time-boxed, since an
// unresponsive dependency is not itself a fatal condition).
const defaultListTimeout = 10 * time.Second

// clusterSubject and vmSubject are the fixed (non-provider-specific) halves
// of each service type's CloudEvents attributes (DD-071); Source is
// provider-specific and built per-Poller in New from the caller-supplied
// provider names (REQ-PUBLISH-030).
const (
	clusterSubject = "dcm.cluster"
	vmSubject      = "dcm.vm"
)

// clustersClient is the subset of publicv1.ClustersClient this package
// depends on, narrowed for testability with a hand-written fake (CLAUDE.md:
// no mocking framework).
type clustersClient interface {
	List(ctx context.Context, in *publicv1.ClustersListRequest, opts ...grpc.CallOption) (*publicv1.ClustersListResponse, error)
}

// computeInstancesClient is the subset of publicv1.ComputeInstancesClient
// this package depends on.
type computeInstancesClient interface {
	List(ctx context.Context, in *publicv1.ComputeInstancesListRequest, opts ...grpc.CallOption) (*publicv1.ComputeInstancesListResponse, error)
}

// publisher is the subset of *statuspublisher.Publisher this package
// depends on.
type publisher interface {
	Publish(st statuspublisher.ServiceType, resourceID, status, message string)
}

// cachedStatus is the last-known (status, message) reported for a resource
// (REQ-POLL-040).
type cachedStatus struct {
	status  string
	message string
}

// Poller periodically lists every DCM-owned Cluster/VM from OSAC and
// reports new/changed/disappeared resources to a Publisher. The zero value
// is not usable; construct via New.
//
// Implements REQ-POLL-010..100.
type Poller struct {
	clusters         clustersClient
	computeInstances computeInstancesClient
	publisher        publisher
	interval         time.Duration
	resyncEvery      int
	listTimeout      time.Duration
	logger           *slog.Logger

	// clusterServiceType/vmServiceType bind the CloudEvents
	// subject/type/source attributes for each service type (DD-071).
	// Source is built from the caller-supplied provider name (REQ-PUBLISH-030)
	// rather than hardcoded, so it always matches whatever name this SP
	// actually registered under (internal/registration), even if
	// SP_PROVIDER_CLUSTER_NAME/SP_PROVIDER_VM_NAME override the default.
	clusterServiceType statuspublisher.ServiceType
	vmServiceType      statuspublisher.ServiceType

	startOnce sync.Once
	done      chan struct{}

	// clusterCache/vmCache are scoped independently per service type
	// (REQ-POLL-040) and are only ever touched from run's single
	// goroutine — no locking needed.
	clusterCache map[string]cachedStatus
	vmCache      map[string]cachedStatus
	cycle        int
}

// New constructs a Poller. A non-positive cfg.ResyncEvery is treated as 1
// (every cycle resyncs) rather than panicking on a modulo-by-zero. A
// non-positive cfg.ListTimeout falls back to defaultListTimeout.
// clusterProviderName/vmProviderName should be the exact names this SP
// registered under (cfg.Provider.ClusterName/VMName) so published events'
// CloudEvents source always matches the registered provider identity.
func New(clusters clustersClient, computeInstances computeInstancesClient, pub publisher, clusterProviderName, vmProviderName string, cfg config.StatusConfig, logger *slog.Logger) *Poller {
	resyncEvery := cfg.ResyncEvery
	if resyncEvery <= 0 {
		resyncEvery = 1
	}
	listTimeout := cfg.ListTimeout
	if listTimeout <= 0 {
		listTimeout = defaultListTimeout
	}
	return &Poller{
		clusters:           clusters,
		computeInstances:   computeInstances,
		publisher:          pub,
		clusterServiceType: statuspublisher.ServiceType{Subject: clusterSubject, Type: "dcm.status.cluster", Source: "dcm/providers/" + clusterProviderName},
		vmServiceType:      statuspublisher.ServiceType{Subject: vmSubject, Type: "dcm.status.vm", Source: "dcm/providers/" + vmProviderName},
		interval:           cfg.PollInterval,
		resyncEvery:        resyncEvery,
		listTimeout:        listTimeout,
		logger:             logger,
		done:               make(chan struct{}),
		clusterCache:       make(map[string]cachedStatus),
		vmCache:            make(map[string]cachedStatus),
	}
}

// Start launches the poll loop in the background, non-blocking and
// idempotent (REQ-POLL-100).
func (p *Poller) Start(ctx context.Context) {
	p.startOnce.Do(func() {
		go p.run(ctx)
	})
}

// Done returns a channel closed once the poll loop has returned (on ctx
// cancellation).
func (p *Poller) Done() <-chan struct{} {
	return p.done
}

func (p *Poller) run(ctx context.Context) {
	defer close(p.done)
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		p.runCycle(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// runCycle executes one poll cycle for both service types (REQ-POLL-020..090).
// The very first call is cycle 0, which is always a resync cycle
// (REQ-POLL-080) — this naturally subsumes cold start, since every
// resource is by definition new to an empty cache.
func (p *Poller) runCycle(ctx context.Context) {
	resync := p.cycle%p.resyncEvery == 0
	p.pollClusters(ctx, resync)
	p.pollVMs(ctx, resync)
	p.cycle++
}

// pollClusters lists all Clusters, publishes any new/changed one (or every
// one, on a resync cycle), and reports any cached-but-now-absent Cluster as
// DELETED exactly once (REQ-POLL-030, REQ-POLL-040, REQ-POLL-050).
func (p *Poller) pollClusters(ctx context.Context, resync bool) {
	items, err := p.listClusters(ctx)
	if err != nil {
		p.logger.Warn("listing clusters failed, skipping this cycle", "error", err)
		return
	}

	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		id := item.GetId()
		seen[id] = struct{}{}
		status := cluster.MapStatus(nil, item.GetStatus())
		message := clusterMessage(status, item.GetStatus().GetConditions())
		p.publishClusterIfNeeded(id, string(status), message, resync)
	}

	for id := range p.clusterCache {
		if _, ok := seen[id]; ok {
			continue
		}
		p.publisher.Publish(p.clusterServiceType, id, string(v1alpha1.ClusterStatusDELETED), disappearedMessage)
		delete(p.clusterCache, id)
	}
}

func (p *Poller) publishClusterIfNeeded(id, status, message string, resync bool) {
	cached, ok := p.clusterCache[id]
	changed := !ok || cached.status != status || cached.message != message
	if changed || resync {
		p.publisher.Publish(p.clusterServiceType, id, status, message)
	}
	p.clusterCache[id] = cachedStatus{status: status, message: message}
}

// pollVMs mirrors pollClusters for ComputeInstances/VMs.
func (p *Poller) pollVMs(ctx context.Context, resync bool) {
	items, err := p.listComputeInstances(ctx)
	if err != nil {
		p.logger.Warn("listing compute instances failed, skipping this cycle", "error", err)
		return
	}

	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		id := item.GetId()
		seen[id] = struct{}{}
		status := vm.MapStatus(nil, item.GetStatus())
		message := vmMessage(status, item.GetStatus().GetConditions())
		p.publishVMIfNeeded(id, string(status), message, resync)
	}

	for id := range p.vmCache {
		if _, ok := seen[id]; ok {
			continue
		}
		p.publisher.Publish(p.vmServiceType, id, string(v1alpha1.VMStatusDELETED), disappearedMessage)
		delete(p.vmCache, id)
	}
}

func (p *Poller) publishVMIfNeeded(id, status, message string, resync bool) {
	cached, ok := p.vmCache[id]
	changed := !ok || cached.status != status || cached.message != message
	if changed || resync {
		p.publisher.Publish(p.vmServiceType, id, status, message)
	}
	p.vmCache[id] = cachedStatus{status: status, message: message}
}

// listClusters pages through every Cluster matching ownershipFilter
// (REQ-POLL-020). Each page's own List call is bounded by p.listTimeout
// (REQ-POLL-025) so a hung backend can never wedge the poll loop
// indefinitely. Offset advances by the number of items actually received
// (len(items)), not the server-reported resp.GetSize() — a defensive
// choice against a Size/Total mismatch (e.g. a buggy or inconsistent
// response reporting Size=0 while Total>0) that would otherwise stall
// offset forever and loop indefinitely; len(items)==0 always terminates
// the page loop outright, regardless of what Total claims.
func (p *Poller) listClusters(ctx context.Context) ([]*publicv1.Cluster, error) {
	var all []*publicv1.Cluster
	var offset int32
	for {
		listCtx, cancel := context.WithTimeout(ctx, p.listTimeout)
		resp, err := p.clusters.List(listCtx, &publicv1.ClustersListRequest{
			Filter: util.Ptr(ownershipFilter),
			Limit:  util.Ptr(listPageSize),
			Offset: util.Ptr(offset),
		})
		cancel()
		if err != nil {
			return nil, err
		}
		items := resp.GetItems()
		all = append(all, items...)
		if len(items) == 0 {
			break
		}
		offset += int32(len(items))
		if offset >= resp.GetTotal() {
			break
		}
	}
	return all, nil
}

// listComputeInstances pages through every ComputeInstance matching
// ownershipFilter (REQ-POLL-020), mirroring listClusters's own
// timeout/offset-advancement approach exactly (see its doc comment).
func (p *Poller) listComputeInstances(ctx context.Context) ([]*publicv1.ComputeInstance, error) {
	var all []*publicv1.ComputeInstance
	var offset int32
	for {
		listCtx, cancel := context.WithTimeout(ctx, p.listTimeout)
		resp, err := p.computeInstances.List(listCtx, &publicv1.ComputeInstancesListRequest{
			Filter: util.Ptr(ownershipFilter),
			Limit:  util.Ptr(listPageSize),
			Offset: util.Ptr(offset),
		})
		cancel()
		if err != nil {
			return nil, err
		}
		items := resp.GetItems()
		all = append(all, items...)
		if len(items) == 0 {
			break
		}
		offset += int32(len(items))
		if offset >= resp.GetTotal() {
			break
		}
	}
	return all, nil
}
