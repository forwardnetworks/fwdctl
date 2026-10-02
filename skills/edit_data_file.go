package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const editDataFileName = "edit-data-file"

func init() { Register(editDataFileName, editDataFile) }

type editDataFileInput struct {
	// Action: upload (a new org-wide data file), attach (to a network), detach (from a network).
	Action string `json:"action"`
	// Name is the data file's name: for upload, the new name (Forward lower-cases it); for attach/detach, an existing one, exact.
	Name string `json:"name"`
	// Upload-only:
	NQEName     string   `json:"nqe_name"`
	Description string   `json:"description"`
	FileType    string   `json:"file_type"`
	Headers     []string `json:"headers"`
	Content     string   `json:"content"`
	// NetworkID is the network to attach to or detach from.
	NetworkID string `json:"network_id"`
	Apply     bool   `json:"apply"`
}

const maxDataFileContent = 500_000 // bytes; Forward's own cap is 50MB, this keeps the input to a size a tool call should carry

var dataFileNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

var dataFileTypes = []string{"CSV", "JSON", "XML", "YAML", "TEXT"} // not XLSX (binary) or STIG (its own fixed-name file, not handled here)

func dfEvidence(action string, detail map[string]any) []result.Evidence {
	return []result.Evidence{result.NewEvidence(result.EvState, "dataFiles", nil, detail, "")}
}

// editDataFile manages the organization's uploaded data files (CSV/JSON/XML/YAML/TEXT datasets a query joins as network.extensions.<nqe_name>)
// and which networks carry one: upload a new file, attach an existing one to a network, or detach it. A file is an ORGANIZATION-wide object
// (Forward lower-cases its name and the upload is visible to every network that attaches it); attaching or detaching affects one network's
// LATER snapshots only, never the file itself or any other network.
func editDataFile(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in editDataFileInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	cx := result.Context{NetworkID: in.NetworkID, Scope: "account", State: "current"}
	mode := result.ModeDryRun
	if in.Apply {
		mode = result.ModeApplied
	}
	switch in.Action {
	case "upload":
		return planDataFileUpload(ctx, s, in, cx, mode)
	case "attach", "detach":
		return planDataFileAttachment(ctx, s, in, cx, mode)
	default:
		return result.Result{}, fmt.Errorf("%w: action must be upload, attach or detach", ErrInvalidInput)
	}
}

func planDataFileUpload(ctx context.Context, s *fwd.Session, in editDataFileInput, cx result.Context, mode string) (result.Result, error) {
	if in.NetworkID != "" {
		return result.Result{}, fmt.Errorf("%w: upload is organization-wide; network_id belongs to attach or detach", ErrInvalidInput)
	}
	if in.Name == "" || !dataFileNameRe.MatchString(in.Name) {
		return result.Result{}, fmt.Errorf("%w: upload needs name (letters, digits, dot, underscore, hyphen)", ErrInvalidInput)
	}
	ft := strings.ToUpper(strings.TrimSpace(in.FileType))
	if !slices.Contains(dataFileTypes, ft) {
		extra := ""
		switch {
		case strings.EqualFold(in.FileType, "XLSX"):
			extra = " (XLSX is a binary format and cannot be carried as JSON text content; upload it through Forward's UI)"
		case strings.EqualFold(in.FileType, "STIG"):
			extra = " (the STIG policy file has a fixed name and its own template; this skill does not manage it)"
		}
		return result.Result{}, fmt.Errorf("%w: file_type must be one of %s%s", ErrInvalidInput, strings.Join(dataFileTypes, ", "), extra)
	}
	if len(in.Headers) > 0 && ft != "CSV" {
		return result.Result{}, fmt.Errorf("%w: headers apply only to file_type CSV", ErrInvalidInput)
	}
	if strings.TrimSpace(in.Content) == "" {
		return result.Result{}, fmt.Errorf("%w: upload needs content (the file's text)", ErrInvalidInput)
	}
	if len(in.Content) > maxDataFileContent {
		return result.Result{}, fmt.Errorf("%w: content is %d bytes; at most %d", ErrInvalidInput, len(in.Content), maxDataFileContent)
	}
	nqeName := in.NQEName
	if nqeName == "" {
		nqeName = strings.TrimSuffix(in.Name, "."+strings.ToLower(ft))
	}
	existing, err := s.DataFiles(ctx)
	if err != nil {
		return result.Result{}, err
	}
	lower := strings.ToLower(in.Name)
	for _, f := range existing {
		if f.Name == lower {
			return result.Build(editDataFileName, result.Failed, fmt.Sprintf("Refused, nothing was changed: a data file named %q already exists (type %s, nqe_name %s)", lower, f.Type, f.NQEName),
				result.Deterministic, cx, result.Options{Mode: mode, Limits: []string{"names are lower-cased and unique; use attach to add the existing file to a network"},
					Evidence: dfEvidence("upload", map[string]any{"name": lower, "existing_type": f.Type})})
		}
	}
	after := map[string]any{"name": lower, "nqe_name": nqeName, "type": ft, "content_bytes": len(in.Content)}
	if len(in.Headers) > 0 {
		after["headers"] = in.Headers
	}
	limits := []string{"a data file is organization-wide: once uploaded, any network can attach it. Content is validated by Forward on upload, not previewed here first (inspect-collection view config with data_file previews an existing file's inferred schema, not content you have not uploaded yet)",
		"this build's SDK has no delete route for a data file: once uploaded there is no undo through this skill. Remove it from Forward's UI, or ask for the delete route to be added"}
	ch := result.Change{Action: "upload_data_file", Target: "data file " + lower, Before: nil, After: after, Reversible: false, Undo: "none: no SDK route deletes a data file (see limits)"}
	if !in.Apply {
		return result.Build(editDataFileName, result.OK, fmt.Sprintf("Dry run: would upload data file %q (%s, nqe_name %s). Nothing was changed; run again with apply=true", lower, ft, nqeName),
			result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Limits: limits, Evidence: dfEvidence("upload", after)})
	}
	created, err := s.AddDataFile(ctx, forward.DataFileCreateRequest{Name: in.Name, NQEName: in.NQEName, Description: in.Description, FileType: forward.DataFileType(ft), Headers: in.Headers}, []byte(in.Content))
	if err != nil {
		return result.Result{}, fmt.Errorf("the upload failed, nothing is known to have changed: %w", err)
	}
	ch.Applied = true
	now, rerr := s.DataFiles(ctx)
	if rerr != nil {
		return result.Result{}, fmt.Errorf("the upload was sent but reading the data files back failed, so it is not proven: %w", rerr)
	}
	held := slices.ContainsFunc(now, func(f forward.DataFile) bool { return f.Name == created.Name })
	if !held {
		return result.Build(editDataFileName, result.Failed, fmt.Sprintf("Forward accepted the upload but %q is not listed back", created.Name), result.Deterministic, cx,
			result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Limits: limits, Evidence: dfEvidence("upload", after)})
	}
	return result.Build(editDataFileName, result.OK, fmt.Sprintf("Uploaded data file %q (nqe_name %s, type %s), attached to no network yet; attach it to a network to collect it", created.Name, created.NQEName, created.Type),
		result.Deterministic, cx, result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Limits: limits, NextActions: []string{"inspect-collection"},
			Evidence: dfEvidence("upload", map[string]any{"name": created.Name, "nqe_name": created.NQEName, "type": created.Type})})
}

func planDataFileAttachment(ctx context.Context, s *fwd.Session, in editDataFileInput, cx result.Context, mode string) (result.Result, error) {
	if in.Name == "" || in.NetworkID == "" {
		return result.Result{}, fmt.Errorf("%w: %s needs name and network_id", ErrInvalidInput, in.Action)
	}
	if in.NQEName != "" || in.Description != "" || in.FileType != "" || len(in.Headers) > 0 || in.Content != "" {
		return result.Result{}, fmt.Errorf("%w: nqe_name, description, file_type, headers and content belong to upload", ErrInvalidInput)
	}
	existing, err := s.DataFiles(ctx)
	if err != nil {
		return result.Result{}, err
	}
	var file *forward.DataFile
	for i := range existing {
		if existing[i].Name == in.Name {
			file = &existing[i]
		}
	}
	if file == nil {
		return result.Build(editDataFileName, result.Failed, fmt.Sprintf("Refused, nothing was changed: no data file named %q (names are exact; inspect-collection view config lists them)", in.Name),
			result.Deterministic, cx, result.Options{Mode: mode, Evidence: dfEvidence(in.Action, map[string]any{"name": in.Name, "found": false})})
	}
	if file.Type == forward.DataFileSTIG {
		msg := map[string]string{"attach": "the STIG policy file is included in every network by default and cannot be attached by name", "detach": "the STIG policy file cannot be excluded from any network"}[in.Action]
		return result.Build(editDataFileName, result.Failed, "Refused, nothing was changed: "+msg, result.Deterministic, cx,
			result.Options{Mode: mode, Evidence: dfEvidence(in.Action, map[string]any{"name": in.Name, "type": "STIG"})})
	}
	attached := slices.Contains(file.NetworkIDs, in.NetworkID)
	before := map[string]any{"attached": attached}
	if (in.Action == "attach" && attached) || (in.Action == "detach" && !attached) {
		return result.Build(editDataFileName, result.OK, fmt.Sprintf("Data file %q is already %s network %s; nothing to change", in.Name, map[string]string{"attach": "attached to", "detach": "detached from"}[in.Action], in.NetworkID),
			result.Deterministic, cx, result.Options{Mode: mode, Evidence: []result.Evidence{result.NewEvidence(result.EvState, "dataFiles", nil, before, "")}})
	}
	after := map[string]any{"attached": in.Action == "attach"}
	undo := map[string]string{"attach": "detach", "detach": "attach"}[in.Action] + fmt.Sprintf(" data file %s from/to network %s again", in.Name, in.NetworkID)
	limits := []string{"this changes which networks carry the file; the file itself and every other network are untouched",
		"a query sees the effect only from the network's NEXT snapshot onward: network.extensions." + file.NQEName + ".status reads MISSING until one is collected after this change"}
	ch := result.Change{Action: in.Action + "_data_file", Target: fmt.Sprintf("data file %s on network %s", in.Name, in.NetworkID), Before: before, After: after, Reversible: true, Undo: undo}
	if !in.Apply {
		return result.Build(editDataFileName, result.OK, fmt.Sprintf("Dry run: would %s data file %q %s network %s. Nothing was changed; run again with apply=true", in.Action, in.Name, map[string]string{"attach": "to", "detach": "from"}[in.Action], in.NetworkID),
			result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Limits: limits, Evidence: dfEvidence(in.Action, before)})
	}
	if in.Action == "attach" {
		err = s.AttachDataFile(ctx, in.NetworkID, in.Name)
	} else {
		err = s.DetachDataFile(ctx, in.NetworkID, in.Name)
	}
	if err != nil {
		return result.Result{}, fmt.Errorf("the %s failed, nothing is known to have changed: %w", in.Action, err)
	}
	ch.Applied = true
	names, rerr := s.DataFilesForNetwork(ctx, in.NetworkID)
	if rerr != nil {
		return result.Result{}, fmt.Errorf("the %s was sent but reading the network's data files back failed, so it is not proven: %w", in.Action, rerr)
	}
	nowAttached := slices.Contains(names, in.Name)
	if nowAttached != (in.Action == "attach") {
		return result.Build(editDataFileName, result.Failed, fmt.Sprintf("Forward accepted the %s but network %s %s %q", in.Action, in.NetworkID, map[bool]string{true: "still lists", false: "does not list"}[nowAttached], in.Name),
			result.Deterministic, cx, result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Limits: limits, Evidence: dfEvidence(in.Action, map[string]any{"name": in.Name, "now_attached": nowAttached})})
	}
	verb := map[string]string{"attach": "Attached", "detach": "Detached"}[in.Action]
	return result.Build(editDataFileName, result.OK, fmt.Sprintf("%s data file %q %s network %s (read back)", verb, in.Name, map[string]string{"attach": "to", "detach": "from"}[in.Action], in.NetworkID),
		result.Deterministic, cx, result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Limits: limits, NextActions: []string{"inspect-collection", "edit-collection"},
			Evidence: dfEvidence(in.Action, after)})
}
