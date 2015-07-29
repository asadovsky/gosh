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
// - Provide simple "unified binary" mechanism, with a function registry
// - Provide hooks for "go test"
// - Provide a means for introspection, to see which commands are running and
//   ask those commands about themselves
// - Pushd/Popd
// - TempFile/TempDir (with cleanup)
// - BinDir, and facilities for building binaries (e.g. Go binaries)

import (
	"fmt"
	"sort"
	"strings"
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

	// Set sets the given env vars, of the form "KEY=value" or "KEY=".
	Set(vars ...string)

	// Get returns the value of the given env var.
	Get(name string) string

	// Env returns this Shell's env vars, excluding preexisting vars.
	Env() []string

	// AddArgs updates this Shell to append the given args to all subsequent
	// commands that it runs.
	AddArgs(args ...string)

	// Wait waits for all commands started by this Shell to complete. Returns nil
	// if all commands ran successfully. Otherwise, returns some command's error.
	Wait() error
}

// TODO: Take options, e.g. *testing.T and panicOnError.
func New() (Shell, func()) {
	sh := &shell{
		vars: map[string]string{},
		args: []string{},
	}
	return sh, func() {
		// FIXME
		// sh.cleanup()
	}
}

type cmd struct {
	name string
	args []string
}

type shell struct {
	vars map[string]string
	args []string
}

func (c *cmd) Run() error {
	// FIXME
	return nil
}

func (c *cmd) Start() error {
	// FIXME
	return nil
}

func (c *cmd) Wait() error {
	// FIXME
	return nil
}

func (sh *shell) Cmd(name string, args ...string) Cmd {
	return &cmd{name: name, args: args}
}

func (sh *shell) Set(vars ...string) {
	for _, v := range vars {
		kv := strings.Split(v, "=")
		// TODO: If *testing.T was provided, fail test instead of panicking.
		if len(kv) != 2 {
			panic(v)
		}
		if kv[1] == "" {
			delete(sh.vars, kv[0])
		} else {
			sh.vars[kv[0]] = kv[1]
		}
	}
}

func (sh *shell) Get(name string) string {
	return sh.vars[name]
}

func (sh *shell) Env() []string {
	res := make([]string, 0, len(sh.vars))
	for k, v := range sh.vars {
		res = append(res, fmt.Sprintf("%s=%s", k, v))
	}
	sort.Strings(res)
	return res
}

func (sh *shell) AddArgs(args ...string) {
	// FIXME
}

func (sh *shell) Wait() error {
	// FIXME
	return nil
}
