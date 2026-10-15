package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	run := func(stdin string, args ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := validate(args, strings.NewReader(stdin), &out, &errOut)
		return code, out.String(), errOut.String()
	}

	t.Run("the example file", func(t *testing.T) {
		code, out, errOut := run("", filepath.Join("..", "..", "examples", "monitors.yaml"))
		if code != 0 || !strings.Contains(out, "valid") {
			t.Fatalf("exit %d\n%s%s", code, out, errOut)
		}
		// It uses ${PG_PASSWORD} and friends, which aren't set here.
		if !strings.Contains(errOut, "warning:") {
			t.Errorf("expected warnings about unset variables:\n%s", errOut)
		}
	})

	bad := `healthchecks:
  - name: API
    type: http
    target: https://example.com
    interval: 5s
  - name: DB
    type: postgres
    target: postgres://u:${VALIDATE_TEST_PW}@db:5432/app
  - name: API
    type: tcp
    target: example.com:443
heartbeats:
  - name: Backup
    every: 1h
    grace: soon
`
	t.Run("every problem is reported", func(t *testing.T) {
		code, _, errOut := run(bad, "-")
		if code != 1 {
			t.Fatalf("exit %d, want 1", code)
		}
		for _, want := range []string{"interval must be at least 10 seconds", "grace", `two monitors are named "API"`, "3 problems"} {
			if !strings.Contains(errOut, want) {
				t.Errorf("missing %q in:\n%s", want, errOut)
			}
		}
	})

	t.Run("json and strict env", func(t *testing.T) {
		os.Unsetenv("VALIDATE_TEST_PW")
		db := "healthchecks:\n  - name: DB\n    type: postgres\n    target: postgres://u:${VALIDATE_TEST_PW}@db:5432/app\n"
		code, out, _ := run(db, "--format", "json", "--strict-env", "-")
		var r validationReport
		if err := json.Unmarshal([]byte(out), &r); err != nil {
			t.Fatal(err, out)
		}
		if code != 1 || r.Valid || len(r.Errors) != 1 || !strings.Contains(r.Errors[0], "VALIDATE_TEST_PW") {
			t.Fatalf("exit %d: %+v", code, r)
		}
		t.Setenv("VALIDATE_TEST_PW", "x")
		if code, out, _ := run(db, "--format", "json", "--strict-env", "-"); code != 0 || !strings.Contains(out, `"valid": true`) {
			t.Fatalf("with the variable set: exit %d %s", code, out)
		}
	})

	t.Run("usage", func(t *testing.T) {
		t.Setenv("MONITORS_FILE", "")
		t.Setenv("MONITORS_YAML", "")
		if code, _, _ := run(""); code != 2 {
			t.Errorf("no file: exit %d, want 2", code)
		}
		if code, _, _ := run("", "--format", "xml", "-"); code != 2 {
			t.Errorf("bad format: exit %d, want 2", code)
		}
	})
}
