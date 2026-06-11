package web

import (
	"encoding/json"
	"sync"
	"time"
)

type jobStatus string

const (
	jobRunning jobStatus = "running"
	jobDone    jobStatus = "done"
	jobError   jobStatus = "error"
)

// job is one in-flight or completed sync. The 42 snapshot lives here (in RAM)
// only until the job is claimed at sign-up, downloaded, or swept by TTL.
type job struct {
	mu           sync.Mutex
	status       jobStatus
	step         int
	total        int
	stepName     string
	snapshot     map[string]json.RawMessage
	ftID         int64
	ftLogin      string
	matchedID    int64  // existing account this 42 identity belongs to, 0 if none
	matchedLogin string // that account's profile login, for the post-sync sign-in link
	errMsg       string
	clientKey    string // who started it, for the per-client concurrency cap (set once)
	createdAt    time.Time
	finishedAt   time.Time // zero until done/error
}

// setProgress is passed to fetch.Pull as its progress callback.
func (j *job) setProgress(step, total int, name string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.step, j.total, j.stepName = step, total, name
}

func (j *job) finish(snapshot map[string]json.RawMessage, ftID int64, ftLogin string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.status, j.snapshot, j.ftID, j.ftLogin = jobDone, snapshot, ftID, ftLogin
	j.step, j.finishedAt = j.total, time.Now()
}

func (j *job) fail(err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.status, j.errMsg, j.finishedAt = jobError, err.Error(), time.Now()
}

// linkAccount records that this job's 42 identity belongs to an existing account
// (set after the pull when a logged-out sync turns out to be a returning user).
func (j *job) linkAccount(id int64, login string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.matchedID, j.matchedLogin = id, login
}

// matched returns the linked account's id and login (id 0 if none).
func (j *job) matched() (int64, string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.matchedID, j.matchedLogin
}

// jobState is the JSON shape streamed to the browser over SSE.
type jobState struct {
	Status string `json:"status"` // running | done | error
	Step   int    `json:"step"`
	Total  int    `json:"total"`
	Name   string `json:"name"`
	Error  string `json:"error,omitempty"`
	// Matched is true when this 42 identity is already registered, so the page
	// offers "sign in" instead of "sign up" once done.
	Matched bool `json:"matched,omitempty"`
}

func (j *job) state() jobState {
	j.mu.Lock()
	defer j.mu.Unlock()
	return jobState{Status: string(j.status), Step: j.step, Total: j.total, Name: j.stepName, Error: j.errMsg, Matched: j.matchedID != 0}
}

// result returns the completed snapshot and identity; ok is false unless done.
func (j *job) result() (snapshot map[string]json.RawMessage, ftID int64, ftLogin string, ok bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.status != jobDone {
		return nil, 0, "", false
	}
	return j.snapshot, j.ftID, j.ftLogin, true
}

// jobRegistry is an in-memory, TTL-evicted, size-capped map of jobs.
type jobRegistry struct {
	mu      sync.Mutex
	jobs    map[string]*job
	maxJobs int
	ttl     time.Duration
}

func newJobRegistry() *jobRegistry {
	r := &jobRegistry{jobs: map[string]*job{}, maxJobs: 1000, ttl: 30 * time.Minute}
	go r.sweepLoop()
	return r
}

// create registers a new running job for clientKey and returns its id. It refuses
// (ok=false) when that client already has a running job, so one client can't spawn
// concurrent syncs that each burn 42 API budget before the per-42-user cooldown can
// bite. A blank clientKey skips the cap. If the registry is at capacity, the oldest
// job is evicted first (bounds worst-case RAM).
func (r *jobRegistry) create(clientKey string) (id string, j *job, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if clientKey != "" && r.hasRunningForLocked(clientKey) {
		return "", nil, false
	}
	if len(r.jobs) >= r.maxJobs {
		r.evictOldestLocked()
	}
	id = randomToken()
	j = &job{status: jobRunning, clientKey: clientKey, createdAt: time.Now()}
	r.jobs[id] = j
	return id, j, true
}

// hasRunningForLocked reports whether clientKey already has a running job. The
// caller holds r.mu; taking j.mu under it matches the create/sweep lock order.
func (r *jobRegistry) hasRunningForLocked(clientKey string) bool {
	for _, j := range r.jobs {
		j.mu.Lock()
		match := j.status == jobRunning && j.clientKey == clientKey
		j.mu.Unlock()
		if match {
			return true
		}
	}
	return false
}

func (r *jobRegistry) get(id string) (*job, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	j, ok := r.jobs[id]
	return j, ok
}

func (r *jobRegistry) delete(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.jobs, id)
}

func (r *jobRegistry) evictOldestLocked() {
	var oldestID string
	var oldest time.Time
	for id, j := range r.jobs {
		if oldestID == "" || j.createdAt.Before(oldest) {
			oldestID, oldest = id, j.createdAt
		}
	}
	if oldestID != "" {
		delete(r.jobs, oldestID)
	}
}

func (r *jobRegistry) sweepLoop() {
	for range time.Tick(5 * time.Minute) {
		r.sweep()
	}
}

// sweep evicts jobs that finished more than ttl ago, plus any that have been
// alive for over 2*ttl (a stuck-running guard). Lock order is always r.mu then
// j.mu, matching create/get/delete which take only r.mu.
func (r *jobRegistry) sweep() {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, j := range r.jobs {
		j.mu.Lock()
		fin, created := j.finishedAt, j.createdAt
		j.mu.Unlock()
		if (!fin.IsZero() && now.Sub(fin) > r.ttl) || now.Sub(created) > 2*r.ttl {
			delete(r.jobs, id)
		}
	}
}
