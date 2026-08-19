// Package cluster implements the OSAC Service Provider's thin
// StrictServerInterface handlers for the 4 Cluster REST operations,
// delegating business logic to internal/cluster and reusing
// internal/httperror for every non-2xx response (DD-070).
//
// Implements .ai/specs/osac-sp-m3-cluster-crud.spec.md's
// internal/handlers/cluster half of Topics 4.1-4.4, plus Topic 4.6 (Error
// Mapping).
package cluster

import (
	"context"
	"log/slog"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
)

// service is the subset of internal/cluster.Service this handler depends
// on. Satisfied by *cluster.Service; kept as an interface so unit tests can
// depend on the package without an import cycle concern and so this
// package never needs to know about Bootstrap/gRPC wiring.
type service interface {
	Create(ctx context.Context, id string, spec v1alpha1.ClusterSpec) (v1alpha1.Cluster, error)
	Get(ctx context.Context, id string) (v1alpha1.Cluster, error)
	List(ctx context.Context, params v1alpha1.ListClustersParams) (v1alpha1.ClusterList, error)
	Delete(ctx context.Context, id string) error
	// SupportsVersion reports whether version has an entry in the
	// service's injected version-translation matrix (REQ-VERSION-070),
	// queried by validateCreateRequest's unsupported-version rejection
	// (REQ-VERSION-080).
	SupportsVersion(version string) bool
}

// Handler implements oapigen.StrictServerInterface's 4 Cluster CRUD
// operations, delegating to svc.
type Handler struct {
	svc    service
	logger *slog.Logger
}

// NewHandler constructs a Handler delegating to svc.
func NewHandler(svc service, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger}
}
