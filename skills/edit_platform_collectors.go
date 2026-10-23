package skills

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// planPlatformCollector registers and deletes the organization's collectors. Registering creates a credential that Forward shows once, so it goes only to the file the caller names (env_file, mode 600, the TOKEN line
// a Docker collector's --env-file takes) and never into a result, a log or the plan; the file is created before the registration, so a path that cannot be written is found before a credential exists.
func planPlatformCollector(ctx context.Context, s *fwd.Session, in editPlatformInput) (*networkPlan, error) {
	list := func(ctx context.Context) ([]forward.Collector, error) {
		v, _, err := s.Client.Collectors.List(ctx)
		return v, err
	}
	cur, err := list(ctx)
	if err != nil {
		return nil, err
	}
	find := func(key string) *forward.Collector {
		for i := range cur {
			if string(cur[i].ID) == key || cur[i].Name == key || cur[i].Username == key {
				return &cur[i]
			}
		}
		return nil
	}
	switch in.Action {
	case "register":
		var def struct {
			Name    *string `json:"name"`
			EnvFile *string `json:"env_file"`
		}
		if err := decodeDefinition(in.Definition, &def, "name, env_file (where the collector's TOKEN line is written, mode 600; must not exist)"); err != nil {
			return nil, err
		}
		if def.Name == nil || strings.TrimSpace(*def.Name) == "" || def.EnvFile == nil || *def.EnvFile == "" {
			return nil, fmt.Errorf("%w: register needs name and env_file", ErrInvalidInput)
		}
		name, path := strings.TrimSpace(*def.Name), *def.EnvFile
		if find(name) != nil {
			return nil, fmt.Errorf("%w: a collector named %q already exists", ErrInvalidInput, name)
		}
		if _, err := os.Lstat(path); err == nil {
			return nil, fmt.Errorf("%w: env_file %s exists; it is never overwritten (the key is shown once), name a new file", ErrInvalidInput, path)
		}
		if st, err := os.Stat(filepath.Dir(path)); err != nil || !st.IsDir() {
			return nil, fmt.Errorf("%w: the directory of env_file %s does not exist", ErrInvalidInput, path)
		}
		var reg *forward.CollectorRegistration
		return &networkPlan{target: "register collector " + name, action: "register_collector", reversible: true,
			after:  map[string]any{"name": name, "env_file": path, "env_file_content": "TOKEN=<the collector's username:password>, mode 600"},
			undo:   "delete the collector (action delete, confirm = its name); stop and remove its container first",
			limits: []string{"the key is written to env_file and nowhere else; Forward shows it once, so keep that file (a collector re-registered under the same name needs a new key)", "registering attaches nothing: run the collector (docker run --network host --env-file FILE, see reference/collectors.md), wait for it to show CONNECTED (fwdctl wait collector), then edit-network object collector action assign"},
			do: func(ctx context.Context) error {
				f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
				if err != nil {
					return fmt.Errorf("env_file: %w", err)
				}
				defer f.Close()
				r, _, err := s.Client.Collectors.Register(ctx, forward.CollectorRegistrationRequest{CollectorName: name})
				if err != nil {
					_ = os.Remove(path)
					return err
				}
				reg = r
				token, err := r.Token()
				if err == nil {
					_, err = f.WriteString("TOKEN=" + token + "\n")
				}
				if err != nil {
					_, _ = s.Client.Collectors.Delete(ctx, string(r.ID))
					_ = os.Remove(path)
					return fmt.Errorf("the key could not be stored, so the registration was deleted again: %v", err)
				}
				return f.Close()
			},
			verify: func(ctx context.Context) (bool, any, error) {
				now, err := list(ctx)
				for _, c := range now {
					if c.Name == name {
						return true, map[string]any{"id": c.ID, "name": c.Name, "username": c.Username, "connection": c.ConnectionStatus}, err
					}
				}
				return false, len(now), err
			},
			learn: func(got any, ch *result.Change) {
				if m, ok := got.(map[string]any); ok {
					ch.After = map[string]any{"id": m["id"], "name": m["name"], "username": m["username"], "env_file": path}
				}
				_ = reg
			}}, nil
	case "delete":
		c := find(in.Name)
		if c == nil {
			return nil, fmt.Errorf("%w: no collector %q (inspect-platform area collectors lists them)", ErrInvalidInput, in.Name)
		}
		before, _ := fwd.Generic(*c)
		id := string(c.ID)
		return &networkPlan{target: "delete collector " + c.Name, action: "delete_collector", before: before, reversible: false, confirm: c.Name,
			limits: []string{"deleting a collector cannot be undone: a new registration has a new username and key. Detach it from its networks first (edit-network object collector); a network that still points at it stops collecting"},
			do:     func(ctx context.Context) error { _, err := s.Client.Collectors.Delete(ctx, id); return err },
			verify: func(ctx context.Context) (bool, any, error) {
				now, err := list(ctx)
				for _, x := range now {
					if string(x.ID) == id {
						return false, x, err
					}
				}
				return true, nil, err
			}}, nil
	case "upgrade":
		return planCollectorUpgrade(ctx, s, in, cur, find, list)
	}
	return nil, fmt.Errorf("%w: collectors takes action register, delete or upgrade", ErrInvalidInput)
}

// planCollectorUpgrade queues the upgrade of one collector (name) or of every collector that can take it (definition {"all": true}): the "update" button Forward's UI shows when a collector is out of date. It only queues:
// Forward waits for a running collection on the collector to finish, then restarts it, and collections queued behind the restart fail with "Collector version changed". PREVIEW: the routes are not in Forward's published spec.
func planCollectorUpgrade(ctx context.Context, s *fwd.Session, in editPlatformInput, cur []forward.Collector, find func(string) *forward.Collector, list func(context.Context) ([]forward.Collector, error)) (*networkPlan, error) {
	var def struct {
		All bool `json:"all"`
	}
	if len(in.Definition) > 0 {
		if err := decodeDefinition(in.Definition, &def, "all (true: every collector that can be upgraded; then give no name)"); err != nil {
			return nil, err
		}
	}
	limits := []string{
		"PREVIEW: Forward's collector upgrade routes are not in its published spec; they need the organization administrator permission MANAGE_COLLECTORS",
		"this only QUEUES the upgrade: it waits for a running collection on that collector to finish, then restarts the collector; collections queued behind the restart fail with \"Collector version changed\", and Forward gives the task 15 minutes. Tell the people who collect through it first",
		"to check: read the collector again (inspect-platform area collectors, or inspect-collection view status) until it is no longer updating, then its update status: UP_TO_DATE means it worked; a failed or timed-out upgrade also stops the updating and leaves OUTDATED, so do not wait for UP_TO_DATE alone",
		"this does not check for a bundled on-prem collector (username \"client\") or a bundled-only deployment, which Forward refuses with 400",
	}
	row := func(c forward.Collector) map[string]any {
		return map[string]any{"name": c.Name, "version": nilIfEmpty(c.Version), "update_status": nilIfEmpty(c.UpdateStatus), "connection": nilIfEmpty(c.ConnectionStatus), "updating": c.CollectorUpdating, "blocker": nilIfEmpty(c.UpgradeBlocker())}
	}
	if def.All {
		if in.Name != "" {
			return nil, fmt.Errorf("%w: all and a collector name are alternatives", ErrInvalidInput)
		}
		var rows []map[string]any
		for _, c := range cur {
			if c.UpgradeBlocker() == "" {
				rows = append(rows, row(c))
			}
		}
		if len(rows) == 0 {
			return nil, nil
		}
		return &networkPlan{target: fmt.Sprintf("queue the upgrade of %d collector(s)", len(rows)), action: "upgrade_collectors", before: rows, after: map[string]any{"queued": len(rows)}, reversible: false, confirm: "all",
			undo: "none: an upgrade is not undone from here", limits: limits,
			do: func(ctx context.Context) error { _, _, err := s.Client.Collectors.UpgradeAll(ctx); return err },
			verify: func(ctx context.Context) (bool, any, error) {
				now, err := list(ctx)
				queued := 0
				for _, c := range now {
					if c.CollectorUpdating || c.UpdateStatus == "UP_TO_DATE" {
						queued++
					}
				}
				return queued > 0, map[string]any{"collectors_updating_or_current": queued}, err
			}}, nil
	}
	c := find(in.Name)
	if c == nil {
		return nil, fmt.Errorf("%w: no collector %q (inspect-platform area collectors lists them)", ErrInvalidInput, in.Name)
	}
	if b := c.UpgradeBlocker(); b != "" {
		return nil, fmt.Errorf("%w: collector %q cannot be upgraded now: %s", ErrInvalidInput, c.Name, b)
	}
	id, name := string(c.ID), c.Name
	return &networkPlan{target: "queue the upgrade of collector " + name, action: "upgrade_collector", before: row(*c), after: map[string]any{"queued": true}, reversible: false, confirm: name,
		undo: "none: an upgrade is not undone from here", limits: limits,
		do: func(ctx context.Context) error { _, err := s.Client.Collectors.Upgrade(ctx, name); return err },
		verify: func(ctx context.Context) (bool, any, error) {
			now, err := list(ctx)
			for _, x := range now {
				if string(x.ID) == id {
					return x.CollectorUpdating || x.UpdateStatus == "UP_TO_DATE", row(x), err
				}
			}
			return false, nil, err
		}}, nil
}
