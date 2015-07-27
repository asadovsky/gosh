package gosh

// TODO:
// - Provide hooks to access Cmd streams (stdin, stdout, stderr)
// - Provide mechanism for capturing variables produced by a process, e.g. a
//   port number
// - Provide mechanism for processes to signal that they are "ready", e.g. ready
//   to serve requests
// - Make it possible to pipe one Cmd's stdout to another's stdin
// - Kill individual commands
// - Have commands kill themselves if their parent Shell's process dies
// - Kill all commands (cleanup routine)
// - Provide a Shell factory that returns a cleanup function
// - Provide simple "unified binary" mechanism, with a function registry
// - Provide hooks for "go test"
// - Provide a means for introspection, to see which commands are running and
//   ask those commands about themselves
// - Pushd/Popd
// - TempFile/TempDir (with cleanup)
// - BinDir, and facilities for building binaries (e.g. Go binaries)
// - Maybe don't return errors anywhere

import (
	"exec"
)

// Cmd represents a command. Errors typically originate from exec.Cmd.
type Cmd interface {
	// Run starts this command and waits for it to complete.
	Run() error

	// Start starts this command.
	Start() error

	// Wait waits for this command to complete.
	Wait() error
}

type Shell interface {
	// Cmd returns a Cmd.
	Cmd(name string, args ...string) Cmd

	// WithVars returns a new Shell that extends this Shell's env vars with the
	// given env vars.
	WithVars(vars ...string) Shell

	// WithArgs returns a new Shell that appends the given args to all commands
	// that it runs.
	WithArgs(args ...string) Shell

	// Env returns this Shell's env vars.
	Env() []string

	// Wait waits for all commands started by this Shell to complete. Returns nil
	// if all commands ran successfully. Otherwise, returns some command's error.
	Wait() error
}
