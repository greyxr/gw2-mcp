package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const maxRecordBytes = 256 << 10

var recordPart = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

type localRecord struct {
	recordSummary
	Data json.RawMessage `json:"data"`
}

type recordSummary struct {
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func registerRecordTools(server *mcp.Server) {
	writeHint := &mcp.ToolAnnotations{ReadOnlyHint: false, OpenWorldHint: boolPtr(false)}
	readHint := &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(false)}
	addTool(server, &mcp.Tool{Name: "put_record", Description: "Explicitly save caller-provided local record data. Records are not fetched or saved automatically; do not include API keys or other secrets.", Annotations: writeHint, InputSchema: map[string]any{
		"type":       "object",
		"properties": map[string]any{"name": map[string]any{"type": "string"}, "kind": map[string]any{"type": "string"}, "data": map[string]any{}},
		"required":   []string{"name", "kind", "data"}, "additionalProperties": false,
	}}, func(_ context.Context, in struct {
		Name string `json:"name"`
		Kind string `json:"kind"`
		Data any    `json:"data"`
	}) (any, error) {
		raw, err := json.Marshal(in.Data)
		if err != nil {
			return nil, err
		}
		record, err := putLocalRecord(in.Name, in.Kind, raw)
		return record.recordSummary, err
	})
	type named struct {
		Name string `json:"name"`
	}
	addTool(server, &mcp.Tool{Name: "get_record", Description: "Read one saved local record, including its caller-provided data.", Annotations: readHint}, func(_ context.Context, in named) (any, error) { return readLocalRecord(in.Name) })
	addTool(server, &mcp.Tool{Name: "list_records", Description: "List saved local record summaries without their data; optionally filter by kind or name.", Annotations: readHint}, func(_ context.Context, in struct {
		Kind string `json:"kind,omitempty"`
		Name string `json:"name,omitempty"`
	}) (any, error) {
		return listLocalRecords(in.Kind, in.Name)
	})
	addTool(server, &mcp.Tool{Name: "delete_record", Description: "Delete a saved local record by name.", Annotations: writeHint}, func(_ context.Context, in named) (any, error) {
		return map[string]string{"deleted": in.Name}, deleteLocalRecord(in.Name)
	})
}

func boolPtr(v bool) *bool { return &v }

func recordsDir() (string, error) {
	if dir := os.Getenv("GW2_MCP_STATE_DIR"); dir != "" {
		return filepath.Clean(dir), nil
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(config, "gw2-mcp", "records"), nil
}

func recordPath(name string, create bool) (string, error) {
	if name != "" && !recordPart.MatchString(name) {
		return "", fmt.Errorf("invalid record name")
	}
	dir, err := recordsDir()
	if err != nil {
		return "", err
	}
	if create {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return "", err
		}
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return "", fmt.Errorf("state path must be a private real directory (0700)")
	}
	if name == "" {
		return dir, nil
	}
	return filepath.Join(dir, name+".json"), nil
}

func regularRecord(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("record must be a regular file")
	}
	return info, err
}

func readLocalRecord(name string) (localRecord, error) {
	path, err := recordPath(name, false)
	if err != nil {
		return localRecord{}, err
	}
	info, err := regularRecord(path)
	if err != nil {
		return localRecord{}, err
	}
	if info.Size() > maxRecordBytes {
		return localRecord{}, fmt.Errorf("record exceeds %d bytes", maxRecordBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return localRecord{}, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxRecordBytes+1))
	if err != nil {
		return localRecord{}, err
	}
	if len(raw) > maxRecordBytes {
		return localRecord{}, fmt.Errorf("record exceeds %d bytes", maxRecordBytes)
	}
	var record localRecord
	if err = json.Unmarshal(raw, &record); err != nil {
		return localRecord{}, err
	}
	if record.Name != name {
		return localRecord{}, fmt.Errorf("record name does not match file")
	}
	return record, nil
}

func putLocalRecord(name, kind string, data json.RawMessage) (localRecord, error) {
	if !recordPart.MatchString(name) || !recordPart.MatchString(kind) || !json.Valid(data) || len(data) > maxRecordBytes || containsConfiguredAPIKey(data, name, kind) {
		return localRecord{}, fmt.Errorf("record requires safe names, valid JSON up to %d bytes, and no configured API key", maxRecordBytes)
	}
	path, err := recordPath(name, true)
	if err != nil {
		return localRecord{}, err
	}
	now := time.Now().UTC()
	record := localRecord{recordSummary: recordSummary{Name: name, Kind: kind, CreatedAt: now, UpdatedAt: now}, Data: data}
	previous, err := readLocalRecord(name)
	if err == nil {
		record.CreatedAt = previous.CreatedAt
	} else if !os.IsNotExist(err) {
		return localRecord{}, err
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return localRecord{}, err
	}
	if len(encoded) > maxRecordBytes {
		return localRecord{}, fmt.Errorf("record exceeds %d bytes", maxRecordBytes)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".record-*.tmp")
	if err != nil {
		return localRecord{}, err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(encoded)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return localRecord{}, err
	}
	return record, os.Rename(tmp.Name(), path)
}

func containsConfiguredAPIKey(data []byte, fields ...string) bool {
	secret := os.Getenv("GW2_API_KEY")
	if secret == "" {
		return false
	}
	for _, field := range fields {
		if strings.Contains(field, secret) {
			return true
		}
	}
	return strings.Contains(string(data), secret)
}

func listLocalRecords(kind, name string) ([]recordSummary, error) {
	if kind != "" && !recordPart.MatchString(kind) || name != "" && !recordPart.MatchString(name) {
		return nil, fmt.Errorf("invalid record kind or name")
	}
	dir, err := recordPath("", false)
	if os.IsNotExist(err) {
		return []recordSummary{}, nil
	}
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	rows := make([]recordSummary, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		entryName := strings.TrimSuffix(entry.Name(), ".json")
		if name != "" && entryName != name {
			continue
		}
		record, err := readLocalRecord(entryName)
		if err != nil {
			return nil, err
		}
		if kind == "" || record.Kind == kind {
			rows = append(rows, record.recordSummary)
		}
	}
	return rows, nil
}

func deleteLocalRecord(name string) error {
	path, err := recordPath(name, false)
	if err != nil {
		return err
	}
	if _, err := regularRecord(path); err != nil {
		return err
	}
	return os.Remove(path)
}
