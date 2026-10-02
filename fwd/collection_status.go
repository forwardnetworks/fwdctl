package fwd

import (
	"context"
	"errors"

	forward "github.com/forwardnetworks/forward-go-sdk"
)

// CollectionProgress is the collection currently running for a network, if any.
func (s *Session) CollectionProgress(ctx context.Context, networkID string) (*forward.CollectionProgress, error) {
	p, _, err := s.Client.CollectorTasks.Progress(ctx, networkID)
	return p, err
}

// RecentTasks lists the network's most recent collector tasks (Forward filters the window before the network, so older
// matching tasks can be outside it).
func (s *Session) RecentTasks(ctx context.Context, networkID string, limit int32) ([]forward.CollectorTask, error) {
	tasks, _, err := s.Client.CollectorTasks.List(ctx, forward.CollectorTaskListOptions{NetworkID: networkID, Limit: &limit})
	if err == nil {
		s.Annotate(intp(len(tasks)), false)
	}
	return tasks, err
}

// DeviceCollectionStatuses reads each device's collection status.
func (s *Session) DeviceCollectionStatuses(ctx context.Context, networkID string) ([]forward.DeviceCollectionStatus, error) {
	st, _, err := s.Client.Collections.DeviceStatuses(ctx, networkID)
	if err == nil {
		s.Annotate(intp(len(st)), false)
	}
	return st, err
}

// CollectorAttachment reads which collector serves the network.
func (s *Session) CollectorAttachment(ctx context.Context, networkID string) (*forward.CollectorAttachment, error) {
	a, _, err := s.Client.Collectors.Attachment(ctx, networkID)
	return a, err
}

// StartCollection starts a network collection and returns its task id. active is true when Forward refused because one is running.
func (s *Session) StartCollection(ctx context.Context, networkID string) (taskID string, active bool, err error) {
	id, _, err := s.Client.CollectorTasks.Start(ctx, networkID)
	if errors.Is(err, forward.ErrCollectionAlreadyInProgress) {
		return "", true, nil
	}
	return id, false, err
}

// CollectorTask reads one collector task. nil means there is no such task.
func (s *Session) CollectorTask(ctx context.Context, taskID string) (*forward.CollectorTask, error) {
	t, _, err := s.Client.CollectorTasks.Get(ctx, taskID)
	if NotFound(err) {
		return nil, nil
	}
	return t, err
}

// StopCollectorTask cancels a running task (CANCEL, no snapshot) or ends it with what was collected (SKIP, makes a snapshot).
func (s *Session) StopCollectorTask(ctx context.Context, taskID, action, note string) error {
	_, err := s.Client.CollectorTasks.Stop(ctx, taskID, action, note)
	return err
}

// CollectorConcurrency says how many devices the network's collector collects at once: the configured value when one is set, the documented default otherwise (isDefault).
// A collector is attached per network; a network with none, or a login that may not read collectors, is an error for the caller to say as a limit.
func (s *Session) CollectorConcurrency(ctx context.Context, networkID string) (name string, concurrency int, isDefault bool, err error) {
	a, err := s.CollectorAttachment(ctx, networkID)
	if err != nil {
		return "", 0, false, err
	}
	if a == nil || (a.CollectorID == "" && a.CollectorName == "") {
		return "", 0, false, errors.New("no collector is attached to this network")
	}
	id := string(a.CollectorID)
	if id == "" {
		id = a.CollectorName
	}
	st, _, err := s.Client.Collectors.GetSettings(ctx, id)
	if err != nil {
		return a.CollectorName, 0, false, err
	}
	if st == nil {
		return a.CollectorName, forward.DefaultCollectorConcurrency, true, nil
	}
	return a.CollectorName, st.EffectiveConcurrency(), st.Concurrency == nil, nil
}
