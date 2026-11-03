package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const maxRecordBytes = 256 << 10

var recordPart = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

type localRecord struct {
	Name      string          `json:"name"`
	Kind      string          `json:"kind"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
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
	mcp.AddTool(server, &mcp.Tool{Name: "put_record", Description: "Explicitly save caller-provided local record data. Records are not fetched or saved automatically; do not include API keys or other secrets.", Annotations: writeHint}, func(_ context.Context, _ *mcp.CallToolRequest, in struct {
		Name string `json:"name"`
		Kind string `json:"kind"`
		Data any    `json:"data"`
	}) (*mcp.CallToolResult, output, error) {
		raw, err := json.Marshal(in.Data)
		if err != nil {
			return nil, output{}, err
		}
		record, err := putLocalRecord(in.Name, in.Kind, raw)
		return nil, output{recordSummary{record.Name, record.Kind, record.CreatedAt, record.UpdatedAt}}, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "get_record", Description: "Read one saved local record, including its caller-provided data.", Annotations: readHint}, func(_ context.Context, _ *mcp.CallToolRequest, in struct {
		Name string `json:"name"`
	}) (*mcp.CallToolResult, output, error) {
		record, err := readLocalRecord(in.Name)
		return nil, output{record}, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "list_records", Description: "List saved local record summaries without their data; optionally filter by kind or name.", Annotations: readHint}, func(_ context.Context, _ *mcp.CallToolRequest, in struct {
		Kind string `json:"kind,omitempty"`
		Name string `json:"name,omitempty"`
	}) (*mcp.CallToolResult, output, error) {
		rows, err := listLocalRecords(in.Kind, in.Name)
		return nil, output{rows}, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "delete_record", Description: "Delete a saved local record by name.", Annotations: writeHint}, func(_ context.Context, _ *mcp.CallToolRequest, in struct {
		Name string `json:"name"`
	}) (*mcp.CallToolResult, output, error) {
		if err := deleteLocalRecord(in.Name); err != nil {
			return nil, output{}, err
		}
		return nil, output{map[string]string{"deleted": in.Name}}, nil
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

func validRecordPart(value, label string) error {
	if !recordPart.MatchString(value) || value == "." || value == ".." {
		return fmt.Errorf("%s must be 1-64 letters, digits, dots, underscores, or hyphens and start with a letter or digit", label)
	}
	return nil
}

func ensureRecordsDir() (string, error) {
	dir, err := recordsDir()
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if err := privateRecordsDir(info); err != nil {
		return "", err
	}
	return dir, nil
}

func existingRecordsDir() (string, error) {
	dir, err := recordsDir()
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if err := privateRecordsDir(info); err != nil {
		return "", err
	}
	return dir, nil
}

func privateRecordsDir(info os.FileInfo) error {
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("state path must be a real directory")
	}
	if info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("state directory permissions must be 0700")
	}
	return nil
}

func readLocalRecord(name string) (localRecord, error) {
	if err := validRecordPart(name, "name"); err != nil {
		return localRecord{}, err
	}
	dir, err := existingRecordsDir()
	if err != nil {
		return localRecord{}, err
	}
	if dir == "" {
		return localRecord{}, os.ErrNotExist
	}
	path := filepath.Join(dir, name+".json")
	info, err := os.Lstat(path)
	if err != nil {
		return localRecord{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return localRecord{}, fmt.Errorf("record must be a regular file")
	}
	if info.Size() > maxRecordBytes {
		return localRecord{}, fmt.Errorf("record exceeds %d bytes", maxRecordBytes)
	}
	f, err := os.Open(path)
	if err != nil {
		return localRecord{}, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxRecordBytes+1))
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
	if err := validRecordPart(name, "name"); err != nil {
		return localRecord{}, err
	}
	if err := validRecordPart(kind, "kind"); err != nil {
		return localRecord{}, err
	}
	if !json.Valid(data) {
		return localRecord{}, fmt.Errorf("data must be valid JSON")
	}
	if len(data) > maxRecordBytes {
		return localRecord{}, fmt.Errorf("data exceeds %d bytes", maxRecordBytes)
	}
	if containsConfiguredAPIKey(data, name, kind) {
		return localRecord{}, fmt.Errorf("record contains the configured API key")
	}
	dir, err := ensureRecordsDir()
	if err != nil {
		return localRecord{}, err
	}
	path := filepath.Join(dir, name+".json")
	now := time.Now().UTC()
	record := localRecord{Name: name, Kind: kind, Data: data, CreatedAt: now, UpdatedAt: now}
	if _, err := os.Lstat(path); err == nil {
		previous, readErr := readLocalRecord(name)
		if readErr != nil {
			return localRecord{}, readErr
		}
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
	tmp, err := os.CreateTemp(dir, ".record-*.tmp")
	if err != nil {
		return localRecord{}, err
	}
	defer os.Remove(tmp.Name())
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(encoded)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return localRecord{}, err
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return localRecord{}, err
	}
	return record, nil
}

func containsConfiguredAPIKey(data []byte, fields ...string) bool {
	secret := os.Getenv("GW2_API_KEY")
	if secret == "" {
		return false
	}
	if bytes.Contains(data, []byte(secret)) {
		return true
	}
	for _, field := range fields {
		if strings.Contains(field, secret) {
			return true
		}
	}
	return false
}

func listLocalRecords(kind, name string) ([]recordSummary, error) {
	if kind != "" {
		if err := validRecordPart(kind, "kind"); err != nil {
			return nil, err
		}
	}
	if name != "" {
		if err := validRecordPart(name, "name"); err != nil {
			return nil, err
		}
	}
	dir, err := existingRecordsDir()
	if err != nil {
		return nil, err
	}
	if dir == "" {
		return []recordSummary{}, nil
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
			rows = append(rows, recordSummary{record.Name, record.Kind, record.CreatedAt, record.UpdatedAt})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows, nil
}

func deleteLocalRecord(name string) error {
	if err := validRecordPart(name, "name"); err != nil {
		return err
	}
	dir, err := existingRecordsDir()
	if err != nil {
		return err
	}
	if dir == "" {
		return os.ErrNotExist
	}
	path := filepath.Join(dir, name+".json")
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("record must be a regular file")
	}
	return os.Remove(path)
}
