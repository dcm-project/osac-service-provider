// Package vm implements the OSAC Service Provider's thin
// StrictServerInterface handlers for the 4 VM REST operations, delegating
// business logic to internal/vm and reusing internal/grpcerror for every
// non-2xx response (DD-126) — the VM counterpart to Milestone 3's
// internal/handlers/cluster.
//
// Implements .ai/specs/osac-sp-m4-vm-crud.spec.md's internal/handlers/vm
// half of Topics 4.1-4.4, plus Topic 4.7 (Error Mapping).
package vm

import (
	"context"
	"log/slog"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
)

// service is the subset of internal/vm.Service this handler depends on.
// Satisfied by *vm.Service; kept as an interface so unit tests can depend
// on the package without an import cycle concern and so this package
// never needs to know about Bootstrap/gRPC wiring.
type service interface {
	Create(ctx context.Context, id string, spec v1alpha1.VMSpec) (v1alpha1.VirtualMachine, error)
	Get(ctx context.Context, id string) (v1alpha1.VirtualMachine, error)
	List(ctx context.Context, params v1alpha1.ListVMsParams) (v1alpha1.VirtualMachineList, error)
	Delete(ctx context.Context, id string) error
}

// Handler implements oapigen.StrictServerInterface's 4 VM CRUD operations,
// delegating to svc.
type Handler struct {
	svc    service
	logger *slog.Logger
}

// NewHandler constructs a Handler delegating to svc.
func NewHandler(svc service, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger}
}
