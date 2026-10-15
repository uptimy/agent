package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/uptimy/agent/internal/filesync"
	"github.com/uptimy/agent/internal/monitor"
)

// validate checks a monitors file without starting the agent, for CI:
//
//	uptimy-agent validate monitors.yaml
//	uptimy-agent validate --strict-env --format json monitors.yaml
//	helm template … | yq '.data["monitors.yaml"]' | uptimy-agent validate -
//
// With no file it checks MONITORS_YAML or MONITORS_FILE, as the agent would
// load them. It exits 0 when the file is valid, 1 when it isn't, and 2 on
// wrong usage.
func validate(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	strictEnv := fs.Bool("strict-env", false, "fail when a ${VAR} a monitor uses isn't set (by default a warning, since CI rarely has the secrets)")
	format := fs.String("format", "text", "text or json")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: uptimy-agent validate [--strict-env] [--format text|json] [file | -]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 1 || (*format != "text" && *format != "json") {
		fs.Usage()
		return 2
	}

	name, data, err := readMonitorsFile(fs.Arg(0), stdin)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	monitors, problems := filesync.Check(data)
	report := validationReport{File: name, Errors: []string{}, Warnings: []string{}}
	for _, p := range problems {
		report.Errors = append(report.Errors, p.Error())
	}
	for _, d := range monitors {
		switch d.Kind {
		case monitor.KindHealthcheck:
			report.Healthchecks++
			for _, v := range d.Check.EnvRefs() {
				if _, ok := os.LookupEnv(v); !ok {
					msg := fmt.Sprintf("%s: environment variable %s isn't set here; the agent needs it", d.Name, v)
					if *strictEnv {
						report.Errors = append(report.Errors, msg)
					} else {
						report.Warnings = append(report.Warnings, msg)
					}
				}
			}
		case monitor.KindHeartbeat:
			report.Heartbeats++
		}
	}
	report.Valid = len(report.Errors) == 0

	if *format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
	} else {
		for _, w := range report.Warnings {
			fmt.Fprintln(stderr, "warning:", w)
		}
		for _, e := range report.Errors {
			fmt.Fprintln(stderr, "error:", e)
		}
		if report.Valid {
			fmt.Fprintf(stdout, "%s: %d healthchecks and %d heartbeats, valid\n", name, report.Healthchecks, report.Heartbeats)
		} else {
			fmt.Fprintf(stderr, "%s: %d %s\n", name, len(report.Errors), plural(len(report.Errors), "problem", "problems"))
		}
	}
	if !report.Valid {
		return 1
	}
	return 0
}

type validationReport struct {
	File         string   `json:"file"`
	Valid        bool     `json:"valid"`
	Healthchecks int      `json:"healthchecks"`
	Heartbeats   int      `json:"heartbeats"`
	Errors       []string `json:"errors"`
	Warnings     []string `json:"warnings"`
}

// readMonitorsFile reads path, stdin for "-", or with no path what the agent
// would load: MONITORS_YAML, else MONITORS_FILE.
func readMonitorsFile(path string, stdin io.Reader) (name string, data []byte, err error) {
	switch {
	case path == "-":
		data, err = io.ReadAll(stdin)
		return "stdin", data, err
	case path != "":
		data, err = os.ReadFile(path) //nolint:gosec // G304: the file the user asked to check
		return path, data, err
	case os.Getenv("MONITORS_YAML") != "":
		return "MONITORS_YAML", []byte(os.Getenv("MONITORS_YAML")), nil
	case os.Getenv("MONITORS_FILE") != "":
		path = os.Getenv("MONITORS_FILE")
		data, err = os.ReadFile(path) //nolint:gosec // G304: the agent's own monitors file
		return path, data, err
	}
	return "", nil, fmt.Errorf("nothing to validate: give a file, - for stdin, or set MONITORS_FILE or MONITORS_YAML")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
