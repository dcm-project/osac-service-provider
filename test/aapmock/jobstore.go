package aapmock

import (
	"sync"
	"time"
)

// job is this mock's in-memory record of a launched AAP job.
//
// canceled is tracked as a separate flag rather than folded into a single
// status-timeline field: from the reconciliation-loop perspective, a job
// that is never canceled always reports status "successful" from its very
// first GetJob poll (DD-213 — no pending/running window to simulate, since
// NFR-TB-030 scopes this mock to the hardware/Ansible boundary, not AAP's
// own job-lifecycle timing). CanCancelJob/CancelJob still need real,
// fail-safe terminal-state semantics for their own direct callers/tests, so
// canceled is tracked independently instead of forcing a fake multi-state
// timeline onto the (never exercised) happy path.
type job struct {
	id        int
	extraVars string
	started   time.Time
	finished  time.Time
	canceled  bool
}

func (j *job) status() string {
	if j.canceled {
		return "canceled"
	}
	return "successful"
}

// jobStore is a thread-safe, ID-keyed in-memory store of launched jobs.
type jobStore struct {
	mu     sync.Mutex
	nextID int
	jobs   map[int]*job
}

func newJobStore() *jobStore {
	return &jobStore{jobs: make(map[int]*job)}
}

// launch records a new job, immediately in its terminal (non-canceled)
// state, and returns its assigned ID (REQ-TB-080).
func (s *jobStore) launch(extraVars string) *job {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	now := time.Now().UTC()
	j := &job{
		id:        s.nextID,
		extraVars: extraVars,
		started:   now,
		finished:  now,
	}
	s.jobs[j.id] = j
	return j
}

func (s *jobStore) get(id int) (*job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	return j, ok
}

// cancel marks a job canceled, returning ok=false if it was already
// terminal (already-canceled — REQ-TB-080's MethodNotAllowedError path,
// DD-213) and found=false if the ID doesn't exist at all.
func (s *jobStore) cancel(id int) (ok bool, found bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	j, exists := s.jobs[id]
	if !exists {
		return false, false
	}
	if j.canceled {
		return false, true
	}
	j.canceled = true
	return true, true
}
