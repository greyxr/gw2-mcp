package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecordLifecycle(t *testing.T) {
	t.Setenv("GW2_MCP_STATE_DIR", filepath.Join(t.TempDir(), "records"))
	first, err := putLocalRecord("account", "valuation", json.RawMessage(`{"coins":12}`))
	check(t, err == nil, "%v", err)
	second, err := putLocalRecord("account", "goal", json.RawMessage(`{"target":5}`))
	check(t, err == nil && second.CreatedAt.Equal(first.CreatedAt), "replacement lost creation date: %+v %v", second, err)
	got, err := readLocalRecord("account")
	check(t, err == nil && got.Kind == "goal" && string(got.Data) == `{"target":5}`, "record: %+v %v", got, err)
	rows, err := listLocalRecords("goal", "account")
	check(t, err == nil && len(rows) == 1 && rows[0].Name == "account", "filter: %+v %v", rows, err)
	rows, err = listLocalRecords("valuation", "")
	check(t, err == nil && len(rows) == 0, "stale kind: %+v %v", rows, err)
	check(t, deleteLocalRecord("account") == nil, "delete failed")
	_, err = readLocalRecord("account")
	check(t, os.IsNotExist(err), "deleted record still exists: %v", err)
}

func TestRecordBoundaries(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "records")
	t.Setenv("GW2_MCP_STATE_DIR", dir)
	t.Setenv("GW2_API_KEY", "secret-test-key")
	for _, input := range []struct{ name, kind, data string }{
		{"../escape", "note", `{}`}, {`a/b`, "note", `{}`}, {`a\b`, "note", `{}`}, {".", "note", `{}`}, {"", "note", `{}`},
		{"valid", "../kind", `{}`}, {"valid", "note", `{oops`},
		{"large", "note", `"` + strings.Repeat("x", maxRecordBytes) + `"`},
		{"secret", "note", `"prefix-secret-test-key-suffix"`},
	} {
		_, err := putLocalRecord(input.name, input.kind, json.RawMessage(input.data))
		check(t, err != nil, "unsafe record accepted: %q %q", input.name, input.kind)
	}
	_, err := listLocalRecords("", "")
	check(t, err == nil, "read missing storage: %v", err)
	_, err = readLocalRecord("missing")
	check(t, os.IsNotExist(err), "missing record: %v", err)
	_, err = os.Stat(dir)
	check(t, os.IsNotExist(err), "reads or rejected writes created storage: %v", err)
	check(t, os.Mkdir(dir, 0755) == nil, "create fixture")
	_, err = putLocalRecord("valid", "note", json.RawMessage(`{}`))
	check(t, err != nil, "accepted shared storage")
	info, err := os.Stat(dir)
	check(t, err == nil && info.Mode().Perm() == 0755, "changed user's directory permissions")
}

func TestRecordsRejectSymlinks(t *testing.T) {
	base := t.TempDir()
	dir, target := filepath.Join(base, "records"), filepath.Join(base, "outside.json")
	t.Setenv("GW2_MCP_STATE_DIR", dir)
	check(t, os.Mkdir(dir, 0700) == nil && os.WriteFile(target, []byte(`{}`), 0600) == nil, "create fixture")
	if err := os.Symlink(target, filepath.Join(dir, "linked.json")); err != nil {
		t.Skip(err)
	}
	_, err := readLocalRecord("linked")
	check(t, err != nil, "read symlink")
	_, err = putLocalRecord("linked", "note", json.RawMessage(`{}`))
	check(t, err != nil && deleteLocalRecord("linked") != nil, "modified symlink")
	link := filepath.Join(base, "directory-link")
	check(t, os.Symlink(dir, link) == nil, "create directory symlink")
	t.Setenv("GW2_MCP_STATE_DIR", link)
	_, err = putLocalRecord("valid", "note", json.RawMessage(`{}`))
	check(t, err != nil, "accepted symlink storage")
}
