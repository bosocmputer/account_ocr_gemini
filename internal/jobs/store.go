// store.go - In-memory job store for async OCR analysis, mirroring the
// map+sync.RWMutex pattern already used by internal/storage/cache.go.

package jobs

import (
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
func Create(reqCtx *common.RequestContext) (job *Job, token string) {
	token = uuid.New().String()
	job = newJob(token, reqCtx)

	storeMutex.Lock()
	store[job.ID] = job
	storeMutex.Unlock()

	startSweeper()
	return job, token
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
