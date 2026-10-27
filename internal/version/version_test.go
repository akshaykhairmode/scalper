package version

import (
	"runtime"
	"strings"
	"testing"
)

func TestString(t *testing.T) {
	oldVersion, oldCommit := Version, Commit
	t.Cleanup(func() { Version, Commit = oldVersion, oldCommit })

	Version, Commit = "v1.2.3", "abc123"
	if got := String(); got != "v1.2.3 (abc123)" {
		t.Fatalf("String() = %q", got)
	}
}

func TestInfo(t *testing.T) {
	info := Info()
	for _, key := range []string{"version", "commit", "build_date", "go_version"} {
		if _, ok := info[key]; !ok {
			t.Fatalf("Info() missing %q", key)
		}
	}
	if info["go_version"] != runtime.Version() {
		t.Fatalf("go_version = %v, want %v", info["go_version"], runtime.Version())
	}
	if v, ok := info["version"].(string); !ok || v == "" {
		t.Fatalf("version = %#v", info["version"])
	}
	if !strings.Contains(runtime.Version(), "go") {
		t.Fatalf("unexpected runtime version %q", runtime.Version())
	}
}
