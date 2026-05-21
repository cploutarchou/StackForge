package rollback

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"stackforge/internal/stackforge/remoteexec"
)

type Record struct {
	ID                 string    `json:"rollback_id"`
	Node               string    `json:"node"`
	NodeAddress        string    `json:"node_address"`
	Component          string    `json:"component"`
	ChangedFiles       []string  `json:"changed_files"`
	BackupFiles        []string  `json:"backup_files"`
	RestoreCommand     string    `json:"restore_command"`
	ManualInstructions string    `json:"manual_instructions"`
	CreatedAt          time.Time `json:"created_at"`
	Reason             string    `json:"reason"`
	Status             string    `json:"status"`
	SafeAutomatic      bool      `json:"safe_automatic"`
}

type ApplyReport struct {
	ID        string    `json:"rollback_id"`
	Status    string    `json:"status"`
	Warnings  []string  `json:"warnings"`
	AppliedAt time.Time `json:"applied_at"`
}

func NewID(node, component string) string {
	return time.Now().UTC().Format("20060102T150405Z") + "-" + node + "-" + component
}

func Save(stateDir string, rec Record) error {
	if rec.ID == "" {
		rec.ID = NewID(rec.Node, rec.Component)
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	if rec.Status == "" {
		rec.Status = "available"
	}
	dir := filepath.Join(stateDir, "rollback")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, rec.ID+".json"), b, 0600)
}

func Load(stateDir, id string) (*Record, error) {
	b, err := os.ReadFile(filepath.Join(stateDir, "rollback", id+".json"))
	if err != nil {
		return nil, err
	}
	var rec Record
	return &rec, json.Unmarshal(b, &rec)
}

func List(stateDir string) ([]Record, error) {
	dir := filepath.Join(stateDir, "rollback")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var rec Record
		if json.Unmarshal(b, &rec) == nil {
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func Apply(ctx context.Context, stateDir, id string, yes bool, exec remoteexec.Executor) (*ApplyReport, error) {
	rec, err := Load(stateDir, id)
	if err != nil {
		return nil, err
	}
	if !yes {
		return nil, fmt.Errorf("rollback apply requires --yes after reviewing rollback %s", id)
	}
	report := &ApplyReport{ID: id, AppliedAt: time.Now().UTC()}
	if !rec.SafeAutomatic {
		report.Status = "refused"
		report.Warnings = append(report.Warnings, rec.ManualInstructions)
		return report, fmt.Errorf("rollback %s is not safe for automatic apply: %s", id, rec.ManualInstructions)
	}
	if exec == nil {
		report.Status = "refused"
		report.Warnings = append(report.Warnings, "no live executor configured; provide --config so StackForge can connect to the node")
		return report, fmt.Errorf("rollback %s requires live executor context", id)
	}
	if rec.NodeAddress == "" {
		report.Status = "refused"
		report.Warnings = append(report.Warnings, "rollback record is missing node_address")
		return report, fmt.Errorf("rollback %s is missing node address", id)
	}
	res, err := exec.Run(ctx, rec.NodeAddress, remoteexec.Command{Command: rec.RestoreCommand, Sudo: true, Timeout: 5 * time.Minute})
	if err != nil {
		report.Status = "failed"
		report.Warnings = append(report.Warnings, res.Stderr)
		return report, err
	}
	rec.Status = "applied"
	_ = Save(stateDir, *rec)
	report.Status = "applied"
	return report, nil
}
