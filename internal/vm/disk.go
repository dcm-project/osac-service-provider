package vm

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

var capacityPattern = regexp.MustCompile(`^(-?[0-9]+(?:\.[0-9]+)?)\s*([A-Za-z]+)$`)

// ParseDiskCapacityGiB parses a DCM capacity string (e.g. "100GB", "2TB")
// into OSAC's size_gib integer, per DD-083: GB/GiB are treated as GiB
// directly (colloquial usage — this SP doesn't distinguish binary from
// decimal units), TB/TiB multiply by 1024, and MB/MiB divide by 1024
// (rounded up, so a sub-GiB disk is never truncated to zero). Unit
// matching is case-insensitive. Returns an error for any other unit, any
// non-positive value, or any string that doesn't parse as
// "<number><unit>".
func ParseDiskCapacityGiB(capacity string) (int32, error) {
	m := capacityPattern.FindStringSubmatch(strings.TrimSpace(capacity))
	if m == nil {
		return 0, fmt.Errorf("invalid disk capacity %q: expected a number followed by a unit (e.g. \"100GB\")", capacity)
	}

	value, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, fmt.Errorf("invalid disk capacity %q: %w", capacity, err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("invalid disk capacity %q: must be positive", capacity)
	}

	var gib float64
	switch strings.ToUpper(m[2]) {
	case "GB", "GIB":
		gib = value
	case "TB", "TIB":
		gib = value * 1024
	case "MB", "MIB":
		gib = value / 1024
	default:
		return 0, fmt.Errorf("invalid disk capacity %q: unrecognized unit %q", capacity, m[2])
	}

	return int32(math.Ceil(gib)), nil
}

// bootDiskName is the reserved disk name identifying the boot disk
// (REQ-VMCREATE-030) — every other disk name is preserved only as an input
// convenience and dropped on translation (SC-M4-002).
const bootDiskName = "boot"

// splitDisks separates spec.storage.disks into OSAC's boot_disk and
// additional_disks, parsing each disk's capacity via ParseDiskCapacityGiB.
// Returns an InvalidArgument error (REQ-VMCREATE-060) if disks doesn't
// contain exactly one disk named "boot", or if any capacity fails to parse
// — checked disk-name cardinality first so a bad boot-disk-selection error
// takes precedence over an unrelated disk's bad capacity.
func splitDisks(disks []v1alpha1.VMDisk) (bootDisk *publicv1.ComputeInstanceDisk, additionalDisks []*publicv1.ComputeInstanceDisk, err error) {
	bootCount := 0
	for _, d := range disks {
		if d.Name == bootDiskName {
			bootCount++
		}
	}
	if bootCount != 1 {
		return nil, nil, invalidArgument(fmt.Sprintf("storage.disks must contain exactly one disk named %q, found %d", bootDiskName, bootCount))
	}

	additionalDisks = make([]*publicv1.ComputeInstanceDisk, 0, len(disks)-1)
	for _, d := range disks {
		sizeGiB, parseErr := ParseDiskCapacityGiB(d.Capacity)
		if parseErr != nil {
			return nil, nil, invalidArgument(fmt.Sprintf("storage.disks[%q]: %s", d.Name, parseErr))
		}
		osacDisk := &publicv1.ComputeInstanceDisk{SizeGib: sizeGiB}
		if d.Name == bootDiskName {
			bootDisk = osacDisk
		} else {
			additionalDisks = append(additionalDisks, osacDisk)
		}
	}
	return bootDisk, additionalDisks, nil
}
