package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalRecordsRoundTripReplaceFilterAndDelete(t *testing.T) {
	t.Setenv("GW2_MCP_STATE_DIR", filepath.Join(t.TempDir(), "records"))
	first, err := putLocalRecord("account-1", "valuation", json.RawMessage(`{"coins":12}`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := readLocalRecord("account-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "valuation" || string(got.Data) != `{"coins":12}` || !got.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("record = %+v", got)
	}
	second, err := putLocalRecord("account-1", "goal", json.RawMessage(`{"target":5}`))
	if err != nil {
		t.Fatal(err)
	}
	if !second.CreatedAt.Equal(first.CreatedAt) || second.Kind != "goal" {
		t.Fatalf("replacement = %+v", second)
	}
	rows, err := listLocalRecords("goal", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "account-1" {
		t.Fatalf("filtered rows = %+v", rows)
	}
	if err = deleteLocalRecord("account-1"); err != nil {
		t.Fatal(err)
	}
	if _, err = readLocalRecord("account-1"); !os.IsNotExist(err) {
		t.Fatalf("after delete error = %v", err)
	}
}

func TestLocalRecordsRejectUnsafeNamesKindsAndJSON(t *testing.T) {
	t.Setenv("GW2_MCP_STATE_DIR", filepath.Join(t.TempDir(), "records"))
	for _, name := range []string{"../escape", `a/b`, `a\b`, ".", ""} {
		if _, err := putLocalRecord(name, "goal", json.RawMessage(`{}`)); err == nil {
			t.Errorf("name %q accepted", name)
		}
	}
	if _, err := putLocalRecord("valid", "../kind", json.RawMessage(`{}`)); err == nil {
		t.Fatal("unsafe kind accepted")
	}
	if _, err := putLocalRecord("valid", "goal", json.RawMessage(`{oops`)); err == nil {
		t.Fatal("invalid JSON accepted")
	}
}

func TestLocalRecordsLimitDataAndRejectSymlinks(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "records")
	t.Setenv("GW2_MCP_STATE_DIR", dir)
	if _, err := putLocalRecord("large", "note", json.RawMessage(`"`+strings.Repeat("x", maxRecordBytes)+`"`)); err == nil {
		t.Fatal("oversized data accepted")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(target, []byte(`{"name":"linked","kind":"note","data":{},"created_at":"2020-01-01T00:00:00Z","updated_at":"2020-01-01T00:00:00Z"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "linked.json")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := readLocalRecord("linked"); err == nil {
		t.Fatal("symlink read accepted")
	}
	if _, err := putLocalRecord("linked", "note", json.RawMessage(`{}`)); err == nil {
		t.Fatal("symlink replacement accepted")
	}
	if err := deleteLocalRecord("linked"); err == nil {
		t.Fatal("symlink deletion accepted")
	}
}

func TestRecordsStateDirectoryRejectsSymlink(t *testing.T) {
	base := t.TempDir()
	link := filepath.Join(base, "records")
	if err := os.Symlink(filepath.Join(base, "target"), link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	t.Setenv("GW2_MCP_STATE_DIR", link)
	if _, err := putLocalRecord("name", "note", json.RawMessage(`{}`)); err == nil {
		t.Fatal("symlink state directory accepted")
	}
}

func TestRecordReadsDoNotCreateStateDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing", "records")
	t.Setenv("GW2_MCP_STATE_DIR", dir)
	if _, err := listLocalRecords("", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := readLocalRecord("missing"); !os.IsNotExist(err) {
		t.Fatalf("get missing record error = %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("read created directory, stat error = %v", err)
	}
}

func TestRecordsRejectSecretAndPreserveDirectoryPermissions(t *testing.T) {
	t.Setenv("GW2_API_KEY", "secret-test-key")
	secretDir := filepath.Join(t.TempDir(), "records")
	t.Setenv("GW2_MCP_STATE_DIR", secretDir)
	if _, err := putLocalRecord("secret", "note", json.RawMessage(`{"value":"prefix-secret-test-key-suffix"}`)); err == nil {
		t.Fatal("configured API key was persisted")
	}
	if _, err := os.Stat(secretDir); !os.IsNotExist(err) {
		t.Fatalf("secret rejection created state directory, stat error = %v", err)
	}
	base := t.TempDir()
	dir := filepath.Join(base, "records")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GW2_MCP_STATE_DIR", dir)
	if _, err := putLocalRecord("note", "note", json.RawMessage(`{}`)); err == nil {
		t.Fatal("permissive existing state directory accepted")
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0755 {
		t.Fatalf("state directory permissions changed to %o", info.Mode().Perm())
	}
}
