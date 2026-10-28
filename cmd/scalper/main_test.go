package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func writeConfig(t *testing.T, listen, dbPath string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := fmt.Sprintf(`app:
  listen: %q
  db_path: %q
  timezone: "Asia/Kolkata"
logging:
  level: "error"
  format: "text"
  file: ""
  max_size_mb: 1
  max_backups: 1
  retention_days: 1
  tick_sample_seconds: 0
  redact: true
kotak:
  mobile_number: "+919999999999"
  ucc: "TESTUCC"
  neo_fin_key: "neotradeapi"
web:
  open_browser: false
`, listen, dbPath)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"-version"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "scalper dev") || !strings.Contains(out, "go=") {
		t.Fatalf("version output = %q", out)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunMissingConfigFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run(nil, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "-config is required") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"-nope"}, &stdout, &stderr); err == nil {
		t.Fatal("expected flag error")
	}
}

func TestRunMissingConfigFile(t *testing.T) {
	var stdout, stderr bytes.Buffer
	missing := filepath.Join(t.TempDir(), "absent.yaml")
	err := run([]string{"-config", missing}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "absent.yaml") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := "app:\n  listen: \"0.0.0.0:8080\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err := run([]string{"-config", path}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "app.listen") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunPaperModeStartsAndShutsDown(t *testing.T) {
	t.Setenv("KOTAK_ACCESS_TOKEN", "")

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := probe.Addr().String()
	if err := probe.Close(); err != nil {
		t.Fatalf("close probe: %v", err)
	}

	path := writeConfig(t, addr, filepath.Join(t.TempDir(), "scalper.db"))
	var stdout, stderr bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- run([]string{"-config", path}, &stdout, &stderr)
	}()

	deadline := time.Now().Add(10 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			if cerr := conn.Close(); cerr != nil {
				t.Fatalf("close probe connection: %v", cerr)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not start on %s; stderr: %s", addr, stderr.String())
		}
		time.Sleep(25 * time.Millisecond)
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("signal: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned %v; stderr: %s", err, stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not shut down")
	}
}

func TestRunBadListen(t *testing.T) {
	t.Setenv("KOTAK_ACCESS_TOKEN", "")
	// A privileged port is not the point here: bind an address that cannot be
	// listened on because the host does not resolve to a local interface.
	path := writeConfig(t, "127.0.0.1:18082", filepath.Join(t.TempDir(), "scalper.db"))
	blocker, err := net.Listen("tcp", "127.0.0.1:18082")
	if err != nil {
		t.Skipf("port unavailable: %v", err)
	}
	defer func() {
		if err := blocker.Close(); err != nil {
			t.Errorf("close blocker: %v", err)
		}
	}()

	var stdout, stderr bytes.Buffer
	err = run([]string{"-config", path}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected listen error")
	}
	if !strings.Contains(err.Error(), "address already in use") {
		t.Fatalf("err = %v", err)
	}
}
