package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/clagentic/clagentic-directory/internal/update"
)

// runUpdate implements the 'update' subcommand against the real host.
func runUpdate(args []string) int {
	return runUpdateWith(args, update.SystemDeps(), os.Stdout, os.Stderr)
}

// runUpdateWith is runUpdate with injectable collaborators for tests.
func runUpdateWith(args []string, deps update.Deps, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(stderr)
	valuesPath := fs.String("values", "", "path to the deployment values file (required)")
	if err := fs.Parse(args); err != nil {
		return update.ExitInvalid
	}
	if *valuesPath == "" {
		fmt.Fprintln(stderr, "update: --values is required")
		return update.ExitInvalid
	}
	v, err := update.LoadValues(*valuesPath)
	if err != nil {
		fmt.Fprintf(stderr, "update: %v\n", err)
		return update.ExitInvalid
	}

	res, err := update.Converge(deps, v)
	printResult(stdout, v.UnitName(), res)
	if err != nil {
		fmt.Fprintf(stderr, "update: %v\n", err)
		var f *update.Failure
		if errors.As(err, &f) {
			return f.Code
		}
		return update.ExitApply
	}
	if !res.Changed() {
		fmt.Fprintln(stdout, "converged: no changes")
	}
	return update.ExitOK
}

func printResult(w io.Writer, unit string, r update.Result) {
	if r.Commit != "" {
		fmt.Fprintf(w, "source: built commit %s\n", r.Commit)
	}
	if r.UnitChanged {
		fmt.Fprintf(w, "changed: unit %s written, daemon-reload run\n", unit)
	}
	if r.BinaryChanged {
		fmt.Fprintln(w, "changed: binary replaced")
	}
	if len(r.Stopped) > 0 {
		fmt.Fprintf(w, "changed: stopped %s\n", strings.Join(r.Stopped, ", "))
	}
	if len(r.Disabled) > 0 {
		fmt.Fprintf(w, "changed: disabled %s\n", strings.Join(r.Disabled, ", "))
	}
	if r.Enabled {
		fmt.Fprintf(w, "changed: enabled %s\n", unit)
	}
	if r.Restarted {
		fmt.Fprintf(w, "changed: restarted %s\n", unit)
	}
	if len(r.Skipped) > 0 {
		fmt.Fprintf(w, "skipped: %s is this tool's own unit and is never stopped or disabled\n", strings.Join(r.Skipped, ", "))
	}
}
