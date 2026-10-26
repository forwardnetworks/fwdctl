package skills

import (
	"context"
	"fmt"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
)

func planSchedule(ctx context.Context, s *fwd.Session, in editSourceInput) (*networkPlan, error) {
	if err := noSecretInputs(in); err != nil {
		return nil, err
	}
	cur, _, err := s.Client.CollectionSchedules.List(ctx, in.NetworkID)
	if err != nil {
		return nil, err
	}
	var def struct {
		Enabled         *bool    `json:"enabled"`
		TimeZone        string   `json:"timeZone"`
		DaysOfTheWeek   []int    `json:"daysOfTheWeek"`
		Times           []string `json:"times"`
		PeriodInSeconds *int     `json:"periodInSeconds"`
		StartAt         string   `json:"startAt"`
		EndAt           string   `json:"endAt"`
	}
	fields := "enabled, timeZone, daysOfTheWeek (0 Sunday to 6 Saturday), times (HH:mm) or periodInSeconds, startAt, endAt (HH:mm, with a period)"
	toDef := func() forward.CollectionScheduleDefinition {
		en := true
		if def.Enabled != nil {
			en = *def.Enabled
		}
		return forward.CollectionScheduleDefinition{Enabled: en, TimeZone: def.TimeZone, DaysOfTheWeek: def.DaysOfTheWeek, Times: def.Times, PeriodInSeconds: def.PeriodInSeconds, StartAt: def.StartAt, EndAt: def.EndAt}
	}
	byID := func(id string) *forward.CollectionSchedule {
		for i := range cur {
			if string(cur[i].ID) == id {
				return &cur[i]
			}
		}
		return nil
	}
	limits := []string{"a collector runs one network collection at a time, so a scheduled run waits if the previous one is still going; the next run time is not returned by Forward", "Forward accepts only its own list of time zones (UTC and regional IDs such as America/Chicago) and answers 400 otherwise; a duplicate schedule is also a 400"}
	switch in.Action {
	case "create":
		if err := decodeDefinition(in.Definition, &def, fields); err != nil {
			return nil, err
		}
		d := toDef()
		return &networkPlan{target: fmt.Sprintf("add a collection schedule to network %s", in.NetworkID), action: "create_schedule", before: cur, after: d, reversible: true, undo: "delete the new schedule (edit-source object schedule action delete, name its id)", limits: limits,
			do: func(ctx context.Context) error {
				_, _, err := s.Client.CollectionSchedules.Create(ctx, in.NetworkID, d)
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				l, _, e := s.Client.CollectionSchedules.List(ctx, in.NetworkID)
				return len(l) > len(cur), nil, e
			}}, nil
	case "update":
		if err := decodeDefinition(in.Definition, &def, fields); err != nil {
			return nil, err
		}
		c := byID(in.Name)
		if c == nil {
			return nil, nil
		}
		d := toDef()
		return &networkPlan{target: fmt.Sprintf("replace collection schedule %s on network %s", in.Name, in.NetworkID), action: "replace_schedule", before: c, after: d, reversible: true, undo: "replace it again with the before values",
			limits: append(limits, "replace restates EVERY field: anything not given is dropped"),
			do: func(ctx context.Context) error {
				_, err := s.Client.CollectionSchedules.Replace(ctx, in.NetworkID, in.Name, d)
				return err
			}}, nil
	case "delete":
		c := byID(in.Name)
		if c == nil {
			return nil, nil
		}
		return &networkPlan{target: fmt.Sprintf("delete collection schedule %s from network %s", in.Name, in.NetworkID), action: "delete_schedule", before: c, after: nil, reversible: true, confirm: in.Name,
			undo:   "create it again with the before values",
			limits: append(limits, "with no schedule left the network is collected only when started by hand (edit-collection)"),
			do: func(ctx context.Context) error {
				_, err := s.Client.CollectionSchedules.Delete(ctx, in.NetworkID, in.Name)
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				l, _, e := s.Client.CollectionSchedules.List(ctx, in.NetworkID)
				for _, x := range l {
					if string(x.ID) == in.Name {
						return false, nil, e
					}
				}
				return true, nil, e
			}}, nil
	}
	return nil, fmt.Errorf("%w: object schedule takes action create, update or delete", ErrInvalidInput)
}
