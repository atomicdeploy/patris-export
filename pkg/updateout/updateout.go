package updateout

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/recorddiff"
	"github.com/atomicdeploy/patris-export/pkg/recordsink"
)

type Config struct {
	Enabled bool              `json:"enabled" yaml:"enabled" toml:"enabled"`
	URL     string            `json:"url,omitempty" yaml:"url,omitempty" toml:"url,omitempty"`
	Method  string            `json:"method,omitempty" yaml:"method,omitempty" toml:"method,omitempty"`
	Format  string            `json:"format,omitempty" yaml:"format,omitempty" toml:"format,omitempty"`
	Mode    string            `json:"mode,omitempty" yaml:"mode,omitempty" toml:"mode,omitempty"`
	Initial bool              `json:"initial" yaml:"initial" toml:"initial"`
	Timeout string            `json:"timeout,omitempty" yaml:"timeout,omitempty" toml:"timeout,omitempty"`
	Headers map[string]string `json:"headers,omitempty" yaml:"headers,omitempty" toml:"headers,omitempty"`
	Command []string          `json:"command,omitempty" yaml:"command,omitempty" toml:"command,omitempty"`
}

type Event struct {
	Type      string                   `json:"type"`
	Timestamp string                   `json:"timestamp"`
	Source    string                   `json:"source,omitempty"`
	Raw       bool                     `json:"raw,omitempty"`
	Records   []map[string]interface{} `json:"records,omitempty"`
	Changes   *recorddiff.ChangeSet    `json:"changes,omitempty"`
	KeyField  string                   `json:"key_field,omitempty"`
}

func Normalize(cfg Config) Config {
	cfg.URL = strings.TrimSpace(cfg.URL)
	cfg.Method = strings.ToUpper(strings.TrimSpace(cfg.Method))
	if cfg.Method == "" {
		cfg.Method = http.MethodPost
	}
	cfg.Format = strings.ToLower(strings.TrimSpace(cfg.Format))
	if cfg.Format == "" {
		cfg.Format = "json"
	}
	if cfg.Format != "csv" {
		cfg.Format = "json"
	}
	cfg.Mode = strings.ToLower(strings.TrimSpace(cfg.Mode))
	switch cfg.Mode {
	case "", "changes", "change", "diff":
		cfg.Mode = "changes"
	case "full", "snapshot", "records":
		cfg.Mode = "full"
	default:
		cfg.Mode = "changes"
	}
	if cfg.Timeout == "" {
		cfg.Timeout = "10s"
	}
	return cfg
}

func Dispatch(ctx context.Context, cfg Config, event Event) error {
	cfg = Normalize(cfg)
	if !cfg.Enabled {
		return nil
	}
	if event.Timestamp == "" {
		event.Timestamp = time.Now().Format(time.RFC3339)
	}
	var errs []string
	if cfg.URL != "" {
		if err := sendHTTP(ctx, cfg, event); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(cfg.Command) > 0 {
		if err := runCommand(ctx, cfg, event); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func sendHTTP(ctx context.Context, cfg Config, event Event) error {
	body, contentType, err := encode(cfg, event)
	if err != nil {
		return err
	}
	timeout, _ := time.ParseDuration(cfg.Timeout)
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, cfg.Method, cfg.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", "patris-export")
	req.Header.Set("X-Patris-Event", event.Type)
	req.Header.Set("X-Patris-Source", event.Source)
	for key, value := range cfg.Headers {
		req.Header.Set(key, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("send update HTTP %s returned %s", cfg.URL, resp.Status)
	}
	return nil
}

func runCommand(ctx context.Context, cfg Config, event Event) error {
	timeout, _ := time.ParseDuration(cfg.Timeout)
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(reqCtx, cfg.Command[0], cfg.Command[1:]...)
	body, _, err := encode(cfg, event)
	if err != nil {
		return err
	}
	cmd.Stdin = bytes.NewReader(body)
	cmd.Env = append(os.Environ(),
		"PATRIS_EXPORT_EVENT_TYPE="+event.Type,
		"PATRIS_EXPORT_EVENT_SOURCE="+event.Source,
		"PATRIS_EXPORT_EVENT_TIMESTAMP="+event.Timestamp,
		"PATRIS_EXPORT_EVENT_KEY_FIELD="+event.KeyField,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("send update command failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func encode(cfg Config, event Event) ([]byte, string, error) {
	if cfg.Mode == "full" || event.Type == "initial" {
		event.Changes = nil
	} else {
		event.Records = nil
	}
	if cfg.Format == "csv" {
		rows := event.Records
		if len(rows) == 0 && event.Changes != nil {
			rows = rowsFromChanges(event.Changes, event.KeyField)
		}
		data, err := recordsink.CSVBytes(rows, event.KeyField)
		return data, "text/csv; charset=utf-8", err
	}
	data, err := json.MarshalIndent(event, "", "  ")
	if err != nil {
		return nil, "", err
	}
	return append(data, '\n'), "application/json; charset=utf-8", nil
}

func rowsFromChanges(changes *recorddiff.ChangeSet, keyField string) []map[string]interface{} {
	if changes == nil {
		return nil
	}
	if strings.TrimSpace(keyField) == "" {
		keyField = changes.KeyField
	}
	if strings.TrimSpace(keyField) == "" {
		keyField = "Code"
	}

	rows := []map[string]interface{}{}
	for _, added := range changes.Added {
		row := copyRow(added)
		row["_change_type"] = "added"
		rows = append(rows, row)
	}
	for _, modified := range changes.Modified {
		row := copyRow(modified.Record)
		if len(row) == 0 {
			row = copyRow(modified.NewValues)
		}
		if _, exists := row[keyField]; !exists {
			row[keyField] = modified.Code
		}
		row["_change_type"] = "modified"
		if len(modified.ChangedFields) > 0 {
			row["_changed_fields"] = strings.Join(modified.ChangedFields, ",")
		}
		rows = append(rows, row)
	}
	for _, deleted := range changes.Deleted {
		rows = append(rows, map[string]interface{}{
			keyField:       deleted,
			"_change_type": "deleted",
		})
	}
	return rows
}

func copyRow(row map[string]interface{}) map[string]interface{} {
	copy := make(map[string]interface{}, len(row)+2)
	for key, value := range row {
		copy[key] = value
	}
	return copy
}
