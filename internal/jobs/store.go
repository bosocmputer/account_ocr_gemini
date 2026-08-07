// store.go - In-memory job store for async OCR analysis, mirroring the
// map+sync.RWMutex pattern already used by internal/storage/cache.go.

package jobs

import (
	"errors"
	"sync"
	"time"

	"github.com/bosocmputer/account_ocr_gemini/internal/common"
	"github.com/google/uuid"
)

type Status string

const (
	StatusProcessing Status = "processing"
	StatusCompleted  Status = "completed"
	StatusFailed     Status = "failed"
)

// JobTTL controls how long a finished job's result stays available for
// polling before the sweeper evicts it. Long enough that a client that was
// briefly disconnected can still fetch the result, short enough that a
// long-running process doesn't accumulate unbounded memory.
const JobTTL = 30 * time.Minute

// SweepInterval is how often the background sweeper checks for expired jobs.
const SweepInterval = 5 * time.Minute

// MaxStoredJobs bounds total memory even within the TTL window — under
// heavy/bursty load (e.g. a user repeatedly hitting "ทดสอบ" while tuning a
// template prompt) many jobs can complete inside a single 30-minute TTL
// window, and each retains its full result until the next sweep. This is a
// hard cap independent of time: Create() evicts the oldest COMPLETED/FAILED
// job to make room when over capacity, and returns an error instead of
// evicting a still-processing job (which would orphan an in-flight AI call —
// wasted spend with no client left able to see the result).
const MaxStoredJobs = 500

var ErrTooManyActiveJobs = errors.New("too many jobs are currently processing, try again shortly")

type Progress struct {
	Percent int    `json:"percent"`
	Stage   string `json:"stage"`
	Message string `json:"message"`
}

type JobError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Job tracks one analyze-receipt request from submission through completion.
// ReqCtx is owned exclusively by the single background goroutine that runs
// the pipeline (matching the existing single-writer discipline already used
// for common.RequestContext elsewhere in this codebase — see EndStep calls
// in handlers.go) and must never be read/written from the HTTP-polling
// goroutine. Status/Progress/Result/Error are the only fields the polling
// handler touches, so only those are guarded by mu.
type Job struct {
	ID        string
	TokenHash string
	ReqCtx    *common.RequestContext
	CreatedAt time.Time

	mu        sync.RWMutex
	status    Status
	progress  Progress
	result    interface{}
	jobErr    *JobError
	updatedAt time.Time
}

func newJob(token string, reqCtx *common.RequestContext) *Job {
	now := time.Now()
	return &Job{
		ID:        uuid.New().String(),
		TokenHash: hashToken(token),
		ReqCtx:    reqCtx,
		CreatedAt: now,
		status:    StatusProcessing,
		progress:  Progress{Percent: 0, Stage: "queued", Message: "รอเริ่มประมวลผล"},
		updatedAt: now,
	}
}

func (j *Job) UpdateProgress(percent int, stage, message string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.progress = Progress{Percent: percent, Stage: stage, Message: message}
	j.updatedAt = time.Now()
}

func (j *Job) Complete(result interface{}) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.status = StatusCompleted
	j.result = result
	j.progress = Progress{Percent: 100, Stage: "completed", Message: "เสร็จสิ้น"}
	j.updatedAt = time.Now()
}

func (j *Job) Fail(code, message string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.status = StatusFailed
	j.jobErr = &JobError{Code: code, Message: message}
	j.updatedAt = time.Now()
}

// Snapshot returns a read-locked copy of the fields the polling handler
// needs, so the caller never holds the lock longer than necessary.
type Snapshot struct {
	Status   Status
	Progress Progress
	Result   interface{}
	Error    *JobError
}

func (j *Job) Snapshot() Snapshot {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return Snapshot{
		Status:   j.status,
		Progress: j.progress,
		Result:   j.result,
		Error:    j.jobErr,
	}
}

func (j *Job) isExpired(now time.Time) bool {
	j.mu.RLock()
	defer j.mu.RUnlock()
	if j.status == StatusProcessing {
		return false // never evict a job that's still running
	}
	return now.Sub(j.updatedAt) > JobTTL
}

var (
	store      = make(map[string]*Job)
	storeMutex sync.RWMutex
	sweepOnce  sync.Once
)

// Create registers a new job and returns it along with a plaintext token the
// caller must return to the client exactly once — only its hash is retained,
// mirroring readepdf's X-Job-Token ownership-proof pattern.
//
// Returns ErrTooManyActiveJobs if the store is at MaxStoredJobs and every
// entry is still processing (nothing evictable without orphaning an
// in-flight AI call) — this is the backpressure signal a caller should
// surface as a 503/retry-later rather than accept the submission.
func Create(reqCtx *common.RequestContext) (job *Job, token string, err error) {
	token = uuid.New().String()
	job = newJob(token, reqCtx)

	storeMutex.Lock()
	if len(store) >= MaxStoredJobs {
		if !evictOldestFinishedLocked() {
			storeMutex.Unlock()
			return nil, "", ErrTooManyActiveJobs
		}
	}
	store[job.ID] = job
	storeMutex.Unlock()

	startSweeper()
	return job, token, nil
}

// evictOldestFinishedLocked removes the oldest COMPLETED/FAILED job to free
// a slot. Must be called with storeMutex already held. Returns false if
// every stored job is still processing (nothing safe to evict).
func evictOldestFinishedLocked() bool {
	var oldestID string
	var oldestUpdatedAt time.Time
	found := false

	for id, job := range store {
		snap := job.Snapshot()
		if snap.Status == StatusProcessing {
			continue
		}
		job.mu.RLock()
		updatedAt := job.updatedAt
		job.mu.RUnlock()
		if !found || updatedAt.Before(oldestUpdatedAt) {
			oldestID = id
			oldestUpdatedAt = updatedAt
			found = true
		}
	}

	if !found {
		return false
	}
	delete(store, oldestID)
	return true
}

// Get looks up a job by ID. Returns (nil, false) if not found OR if the
// supplied token doesn't match — collapsed into one not-found case so a
// wrong token can't be used to probe for the existence of a job ID.
func Get(id, token string) (*Job, bool) {
	storeMutex.RLock()
	job, exists := store[id]
	storeMutex.RUnlock()

	if !exists || job.TokenHash != hashToken(token) {
		return nil, false
	}
	return job, true
}

func startSweeper() {
	sweepOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(SweepInterval)
			defer ticker.Stop()
			for range ticker.C {
				sweep()
			}
		}()
	})
}

func sweep() {
	now := time.Now()
	storeMutex.Lock()
	defer storeMutex.Unlock()
	for id, job := range store {
		if job.isExpired(now) {
			delete(store, id)
		}
	}
}
