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
	}
	return nil, fmt.Errorf("%w: collectors takes action register or delete", ErrInvalidInput)
}
