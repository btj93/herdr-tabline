package herdrapi_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/btj93/herdr-tabline/internal/herdrapi"
)

func TestRecordedFixturesDecodeWithoutDroppingDocumentedFields(t *testing.T) {
	var snapshot herdrapi.Snapshot
	snapshotData := readFixture(t, "snapshot.json")
	decodeJSON(t, snapshotData, &snapshot)
	assertNonNullJSONFieldsPreserved(t, snapshotData, snapshot)
	if len(snapshot.Workspaces) == 0 || len(snapshot.Tabs) == 0 || len(snapshot.Panes) == 0 || len(snapshot.Layouts) == 0 || len(snapshot.Agents) == 0 {
		t.Fatalf("recorded snapshot lost records: %#v", snapshot)
	}
	if snapshot.Workspaces[0].WorkspaceID == "" || snapshot.Workspaces[0].Label == "" || snapshot.Tabs[0].TabID == "" || snapshot.Tabs[0].Label == "" || snapshot.Panes[0].PaneID == "" || snapshot.Panes[0].Cwd == "" || snapshot.Panes[0].TerminalID == "" || snapshot.Panes[0].TerminalTitle == "" || snapshot.Agents[0].PaneID == "" || snapshot.Agents[0].Agent == "" || snapshot.Agents[0].TerminalTitle == "" || len(snapshot.Agents[0].AgentSession) == 0 {
		t.Fatalf("recorded snapshot dropped populated fields: %#v", snapshot)
	}
	for _, name := range []string{"process_info_codex.json", "process_info_claude.json"} {
		var info herdrapi.ProcessInfo
		fixture := readFixture(t, name)
		decodeJSON(t, fixture, &info)
		assertNonNullJSONFieldsPreserved(t, fixture, info)
		if info.PaneID == "" || info.ForegroundProcessGroupID == nil || info.ShellPID == nil || len(info.ForegroundProcesses) == 0 || info.ForegroundProcesses[0].PID == 0 || info.ForegroundProcesses[0].Name == "" || info.ForegroundProcesses[0].Argv0 == "" || len(info.ForegroundProcesses[0].Argv) == 0 || info.ForegroundProcesses[0].CommandLine == "" || info.ForegroundProcesses[0].Cwd == "" {
			t.Fatalf("recorded process fixture %s lost required fields: %#v", name, info)
		}
	}
}

func TestNullableFieldsDecodeAsZeroValues(t *testing.T) {
	var snapshot herdrapi.Snapshot
	if err := json.Unmarshal([]byte(`{"workspaces":[{"workspace_id":"w","label":null,"worktree":null}],"tabs":[{"tab_id":"t","workspace_id":"w","label":null}],"panes":[{"pane_id":"p","tab_id":"t","label":null,"title":null,"terminal_title":null,"agent":null,"display_agent":null}]}`), &snapshot); err != nil {
		t.Fatal(err)
	}
	var info herdrapi.ProcessInfo
	if err := json.Unmarshal([]byte(`{"foreground_process_group_id":null,"shell_pid":null,"tty":null,"foreground_processes":[{"pid":1,"name":"process","argv0":null,"argv":null,"cmdline":null,"cwd":null}]}`), &info); err != nil {
		t.Fatal(err)
	}
	if snapshot.Workspaces[0].Label != "" || snapshot.Workspaces[0].Worktree != nil || snapshot.Tabs[0].Label != "" || snapshot.Panes[0].Title != "" || snapshot.Panes[0].Agent != "" || info.ForegroundProcessGroupID != nil || info.ShellPID != nil || info.TTY != "" || info.ForegroundProcesses[0].Argv0 != "" || len(info.ForegroundProcesses[0].Argv) != 0 || info.ForegroundProcesses[0].CommandLine != "" || info.ForegroundProcesses[0].Cwd != "" {
		t.Fatalf("nullable fields did not normalize to zero values: %#v %#v", snapshot, info)
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func decodeJSON(t *testing.T, data []byte, target any) {
	t.Helper()
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}

// assertNonNullJSONFieldsPreserved verifies that every populated fixture field is
// represented by the raw API type and survives a decode/re-encode round trip.
func assertNonNullJSONFieldsPreserved(t *testing.T, fixture []byte, decoded any) {
	t.Helper()
	var expected, actual any
	if err := json.Unmarshal(fixture, &expected); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &actual); err != nil {
		t.Fatal(err)
	}
	assertPopulatedFields(t, expected, actual, "$")
}

func assertPopulatedFields(t *testing.T, expected, actual any, path string) {
	t.Helper()
	switch want := expected.(type) {
	case nil:
		return
	case map[string]any:
		got, ok := actual.(map[string]any)
		if !ok {
			t.Fatalf("%s: decoded value is %T, want object", path, actual)
		}
		for key, value := range want {
			if value == nil {
				continue
			}
			actualValue, exists := got[key]
			if !exists {
				t.Fatalf("%s.%s: populated fixture field was dropped", path, key)
			}
			assertPopulatedFields(t, value, actualValue, path+"."+key)
		}
	case []any:
		got, ok := actual.([]any)
		if !ok || len(got) != len(want) {
			t.Fatalf("%s: decoded array = %#v, want %d entries", path, actual, len(want))
		}
		for index := range want {
			assertPopulatedFields(t, want[index], got[index], path+"["+string(rune('0'+index))+"]")
		}
	default:
		if expected != actual {
			t.Fatalf("%s: decoded value = %#v, want %#v", path, actual, expected)
		}
	}
}
