// Package mockprovider fakes the OSAC backend side of the gRPC contract
// osac-sp dials (osac.public.v1's Capabilities/Clusters/ComputeInstances/
// Subnets/VirtualNetworks) plus its OIDC discovery-and-token flow, so the
// kind-based e2e infra (osac-service-provider#17, Phase 1 / FLPATH-4759)
// can run a real control-plane + real osac-sp without OSAC's real
// fulfillment-service or Keycloak. See
// .ai/specs/osac-sp-e2e-mock-provider.spec.md.
package mockprovider

import (
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// resourceStore is a thread-safe, ID-keyed, insertion-ordered in-memory
// store shared by every CRUD-shaped OSAC service fake (REQ-MOCK-020/021/
// 040/050/060) — implemented once here rather than duplicated across
// clusters.go/computeinstances.go/subnets.go/virtualnetworks.go.
type resourceStore[T any] struct {
	mu    sync.Mutex
	items map[string]T
	order []string
}

func newResourceStore[T any]() *resourceStore[T] {
	return &resourceStore[T]{items: make(map[string]T)}
}

// create stores obj under the caller-supplied id, failing with
// ALREADY_EXISTS if id is already in use (REQ-MOCK-020). Used by the
// SP-supplied-ID services (Clusters, ComputeInstances).
func (s *resourceStore[T]) create(id string, obj T) (T, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.items[id]; exists {
		var zero T
		return zero, status.Errorf(codes.AlreadyExists, "%q already exists", id)
	}
	s.items[id] = obj
	s.order = append(s.order, id)
	return obj, nil
}

// insert unconditionally stores obj under id, with no duplicate check.
// Used by the server-generated-ID services (Subnets, VirtualNetworks),
// whose caller always generates a fresh id immediately before calling
// this (REQ-MOCK-021) — an ALREADY_EXISTS branch would be dead code there
// (a UUIDv4 collision is not a realistically testable condition), so it
// is simply not offered on this path.
func (s *resourceStore[T]) insert(id string, obj T) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[id] = obj
	s.order = append(s.order, id)
}

func (s *resourceStore[T]) get(id string) (T, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.items[id]
	if !ok {
		var zero T
		return zero, status.Errorf(codes.NotFound, "%q not found", id)
	}
	return obj, nil
}

// list returns items in creation order honoring offset/limit (0
// offset/unset limit == all) — REQ-MOCK-050. total is the full collection
// size, independent of offset/limit.
func (s *resourceStore[T]) list(offset, limit int) (items []T, total int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	total = len(s.order)

	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	ids := s.order[offset:]

	if limit > 0 && limit < len(ids) {
		ids = ids[:limit]
	}

	items = make([]T, 0, len(ids))
	for _, id := range ids {
		items = append(items, s.items[id])
	}
	return items, total
}

func (s *resourceStore[T]) delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[id]; !ok {
		return status.Errorf(codes.NotFound, "%q not found", id)
	}
	delete(s.items, id)
	for i, existing := range s.order {
		if existing == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	return nil
}
