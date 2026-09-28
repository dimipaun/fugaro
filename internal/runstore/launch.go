package runstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Launch is launch.json: proof a run's execution was started (design §4.7).
type Launch struct {
	Version    int       `json:"version"`
	RunID      string    `json:"run_id"`
	Backend    string    `json:"backend"`   // "cloud-run"
	Execution  string    `json:"execution"` // canonical full name (projects/…/executions/<name>); compare only via backend.SameExecution
	Job        string    `json:"job"`
	LogURL     string    `json:"log_url,omitempty"`
	LaunchedBy string    `json:"launched_by,omitempty"`
	LaunchedAt time.Time `json:"launched_at"`
}

// WriteLaunch stores launch.json once; a second write returns ErrExists.
func (s *Store) WriteLaunch(ctx context.Context, l *Launch) error {
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	return s.create(ctx, "launch.json", data, "application/json")
}

// ReadLaunch loads launch.json; ErrNotFound means never launched.
func (s *Store) ReadLaunch(ctx context.Context) (*Launch, error) {
	data, err := s.readRecordObject(ctx, "launch.json")
	if err != nil {
		return nil, err
	}
	var l Launch
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("decoding launch.json: %w", err)
	}
	return &l, nil
}

// Claim is the "launching" marker: a CLI is between task.json and
// launch.json for this run.
type Claim struct {
	Holder string    `json:"holder"`
	At     time.Time `json:"at"`
}

// Claim takes the launch claim. When another holder has it, ok is false and
// existing is theirs.
func (s *Store) Claim(ctx context.Context, holder string, at time.Time) (ok bool, existing *Claim, err error) {
	data, err := json.Marshal(Claim{Holder: holder, At: at.UTC()})
	if err != nil {
		return false, nil, err
	}
	for attempt := 0; ; attempt++ {
		err = s.create(ctx, "launching", data, "application/json")
		if err == nil {
			return true, nil, nil
		}
		if !errors.Is(err, ErrExists) {
			return false, nil, err
		}
		raw, rerr := s.readRecordObject(ctx, "launching")
		if errors.Is(rerr, ErrNotFound) && attempt == 0 {
			continue // released between our create and our read: absent, so try once more (N-10)
		}
		if rerr != nil {
			return false, nil, rerr
		}
		var c Claim
		if jerr := json.Unmarshal(raw, &c); jerr != nil {
			return false, &Claim{}, nil // unreadable: treat as held since the zero time
		}
		// existing is informational (ls, messages). A takeover must judge
		// staleness on its own generation-carrying read (Task 10, N-1).
		return false, &c, nil
	}
}

// ClaimKey is the claim's object name, for Task 10's conditional takeover.
func (s *Store) ClaimKey() string { return s.prefix + "launching" }

// ClaimTTL is how long a launch claim protects a launch in flight. After
// it, the claim is stale: fugaro run may take it over and ls shows the run
// as unlaunched.
const ClaimTTL = 10 * time.Minute

// ReadClaim returns the launch claim, ErrNotFound when there is none, or
// a zero Claim when it can't be parsed.
func (s *Store) ReadClaim(ctx context.Context) (*Claim, error) {
	raw, err := s.readRecordObject(ctx, "launching")
	if err != nil {
		return nil, err
	}
	var c Claim
	if json.Unmarshal(raw, &c) != nil {
		return &Claim{}, nil
	}
	return &c, nil
}
