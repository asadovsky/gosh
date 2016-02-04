// Copyright 2015 The Vanadium Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package gosh provides facilities for running and managing processes: start
// them, wait for them to exit, capture their output streams, pipe messages
// between them, terminate them (e.g. on SIGINT), and so on.
//
// Gosh is meant to be used in situations where one might otherwise be tempted
// to write a shell script. (Oh my gosh, no more shell scripts!)
//
// For usage examples, see shell_test.go and internal/gosh_example/main.go.
package gosh

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math/rand"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

const (
	envBinDir         = "GOSH_BIN_DIR"
	envChildOutputDir = "GOSH_CHILD_OUTPUT_DIR"
	envExitAfter      = "GOSH_EXIT_AFTER"
	envInvocation     = "GOSH_INVOCATION"
	envWatchParent    = "GOSH_WATCH_PARENT"
)

var (
	errAlreadyCalledCleanup = errors.New("gosh: already called Shell.Cleanup")
	errDidNotCallInitMain   = errors.New("gosh: did not call gosh.InitMain")
	errDidNotCallNewShell   = errors.New("gosh: did not call gosh.NewShell")
)

// Shell represents a shell. Not thread-safe.
type Shell struct {
	// Err is the most recent error from this Shell or any of its Cmds (may be
	// nil).
	Err error
	// Opts is the Opts struct for this Shell, with default values filled in.
	Opts Opts
	// Vars is the map of env vars for this Shell.
	Vars map[string]string
	// Args is the list of args to append to subsequent command invocations.
	Args []string
	// Internal state.
	calledNewShell  bool
	cleanupDone     chan struct{}
	cleanupMu       sync.Mutex // protects the fields below; held during cleanup
	calledCleanup   bool
	cmds            []*Cmd
	tempFiles       []*os.File
	tempDirs        []string
	dirStack        []string // for pushd/popd
	cleanupHandlers []func()
}

// Opts configures Shell.
type Opts struct {
	// Fatalf is called whenever an error is encountered.
	// If not specified, defaults to panic(fmt.Sprintf(format, v...)).
	Fatalf func(format string, v ...interface{})
	// Logf is called to log things.
	// If not specified, defaults to log.Printf(format, v...).
	Logf func(format string, v ...interface{})
	// Child stdout and stderr are propagated up to the parent's stdout and stderr
	// iff PropagateChildOutput is true.
	PropagateChildOutput bool
	// If specified, each child's stdout and stderr streams are also piped to
	// files in this directory.
	// If not specified, defaults to GOSH_CHILD_OUTPUT_DIR.
	ChildOutputDir string
	// Directory where BuildGoPkg() writes compiled binaries.
	// If not specified, defaults to GOSH_BIN_DIR.
	BinDir string
}

// NewShell returns a new Shell.
func NewShell(opts Opts) *Shell {
	sh, err := newShell(opts)
	sh.HandleError(err)
	return sh
}

// HandleError sets sh.Err. If err is not nil, it also calls sh.Opts.Fatalf.
func (sh *Shell) HandleError(err error) {
	sh.Ok()
	sh.Err = err
	if err != nil && sh.Opts.Fatalf != nil {
		sh.Opts.Fatalf("%v", err)
	}
}

// Cmd returns a Cmd for an invocation of the named program. The given arguments
// are passed to the child as command-line arguments.
func (sh *Shell) Cmd(name string, args ...string) *Cmd {
	sh.Ok()
	res, err := sh.cmd(nil, name, args...)
	sh.HandleError(err)
	return res
}

// FuncCmd returns a Cmd for an invocation of the given registered Func. The
// given arguments are gob-encoded in the parent process, then gob-decoded in
// the child and passed to the Func as parameters. To specify command-line
// arguments for the child invocation, append to the returned Cmd's Args.
func (sh *Shell) FuncCmd(f *Func, args ...interface{}) *Cmd {
	sh.Ok()
	res, err := sh.funcCmd(f, args...)
	sh.HandleError(err)
	return res
}

// Wait waits for all commands started by this Shell to exit.
func (sh *Shell) Wait() {
	sh.Ok()
	sh.HandleError(sh.wait())
}

// Move moves a file.
func (sh *Shell) Move(oldpath, newpath string) {
	sh.Ok()
	sh.HandleError(sh.move(oldpath, newpath))
}

// BuildGoPkg compiles a Go package using the "go build" command and writes the
// resulting binary to sh.Opts.BinDir, or to the -o flag location if specified.
// Returns the absolute path to the binary.
func (sh *Shell) BuildGoPkg(pkg string, flags ...string) string {
	// TODO(sadovsky): Convert BuildGoPkg into a utility function.
	sh.Ok()
	res, err := sh.buildGoPkg(pkg, flags...)
	sh.HandleError(err)
	return res
}

// MakeTempFile creates a new temporary file in os.TempDir, opens the file for
// reading and writing, and returns the resulting *os.File.
func (sh *Shell) MakeTempFile() *os.File {
	sh.Ok()
	res, err := sh.makeTempFile()
	sh.HandleError(err)
	return res
}

// MakeTempDir creates a new temporary directory in os.TempDir and returns the
// path of the new directory.
func (sh *Shell) MakeTempDir() string {
	sh.Ok()
	res, err := sh.makeTempDir()
	sh.HandleError(err)
	return res
}

// Pushd behaves like Bash pushd.
func (sh *Shell) Pushd(dir string) {
	sh.Ok()
	sh.HandleError(sh.pushd(dir))
}

// Popd behaves like Bash popd.
func (sh *Shell) Popd() {
	sh.Ok()
	sh.HandleError(sh.popd())
}

// AddCleanupHandler registers the given function to be called during cleanup.
// Cleanup handlers are called in LIFO order, possibly in a separate goroutine
// spawned by gosh.
func (sh *Shell) AddCleanupHandler(f func()) {
	sh.Ok()
	sh.HandleError(sh.addCleanupHandler(f))
}

// Cleanup cleans up all resources (child processes, temporary files and
// directories) associated with this Shell. It is safe (and recommended) to call
// Cleanup after a Shell error. It is also safe to call Cleanup multiple times;
// calls after the first return immediately with no effect. Cleanup never calls
// HandleError.
func (sh *Shell) Cleanup() {
	if !sh.calledNewShell {
		panic(errDidNotCallNewShell)
	}
	sh.cleanupMu.Lock()
	defer sh.cleanupMu.Unlock()
	if !sh.calledCleanup {
		sh.cleanup()
	}
}

// Ok panics iff this Shell is in a state where it's invalid to call other
// methods. This method is public to facilitate Shell wrapping.
func (sh *Shell) Ok() {
	if !sh.calledNewShell {
		panic(errDidNotCallNewShell)
	}
	// Panic on incorrect usage of Shell.
	if sh.Err != nil {
		panic(fmt.Errorf("gosh: Shell.Err is not nil: %v", sh.Err))
	}
	sh.cleanupMu.Lock()
	defer sh.cleanupMu.Unlock()
	if sh.calledCleanup {
		panic(errAlreadyCalledCleanup)
	}
}

////////////////////////////////////////
// Internals

// Note: On error, newShell returns a *Shell with Opts.Fatalf initialized to
// simplify things for the caller.
func newShell(opts Opts) (*Shell, error) {
	osVars := sliceToMap(os.Environ())
	if opts.Fatalf == nil {
		opts.Fatalf = func(format string, v ...interface{}) {
			panic(fmt.Sprintf(format, v...))
		}
	}
	if opts.Logf == nil {
		opts.Logf = func(format string, v ...interface{}) {
			log.Printf(format, v...)
		}
	}
	if opts.ChildOutputDir == "" {
		opts.ChildOutputDir = osVars[envChildOutputDir]
	}
	// Filter out any gosh env vars coming from outside.
	shVars := copyMap(osVars)
	for _, key := range []string{envBinDir, envChildOutputDir, envExitAfter, envInvocation, envWatchParent} {
		delete(shVars, key)
	}
	sh := &Shell{
		Opts:           opts,
		Vars:           shVars,
		calledNewShell: true,
		cleanupDone:    make(chan struct{}),
	}
	if sh.Opts.BinDir == "" {
		sh.Opts.BinDir = osVars[envBinDir]
		if sh.Opts.BinDir == "" {
			var err error
			if sh.Opts.BinDir, err = sh.makeTempDir(); err != nil {
				sh.cleanup()
				return sh, err
			}
		}
	}
	sh.cleanupOnSignal()
	return sh, nil
}

// cleanupOnSignal starts a goroutine that calls cleanup if a termination signal
// is received.
func (sh *Shell) cleanupOnSignal() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)
	go func() {
		select {
		case sig := <-ch:
			// A termination signal was received; the process will exit.
			sh.logf("Received signal: %v\n", sig)
			sh.cleanupMu.Lock()
			defer sh.cleanupMu.Unlock()
			if !sh.calledCleanup {
				sh.cleanup()
			}
			// Note: We hold cleanupMu during os.Exit(1) so that the main goroutine
			// will not call Shell.Ok() and panic before we exit.
			os.Exit(1)
		case <-sh.cleanupDone:
			// The user called sh.Cleanup; stop listening for signals and exit this
			// goroutine.
		}
		signal.Stop(ch)
	}()
}

func (sh *Shell) logf(format string, v ...interface{}) {
	if sh.Opts.Logf != nil {
		sh.Opts.Logf(format, v...)
	}
}

func (sh *Shell) cmd(vars map[string]string, name string, args ...string) (*Cmd, error) {
	if vars == nil {
		vars = make(map[string]string)
	}
	c, err := newCmd(sh, mergeMaps(sh.Vars, vars), name, append(args, sh.Args...)...)
	if err != nil {
		return nil, err
	}
	c.PropagateOutput = sh.Opts.PropagateChildOutput
	c.OutputDir = sh.Opts.ChildOutputDir
	return c, nil
}

var executablePath = os.Args[0]

func init() {
	// If exec.LookPath fails, hope for the best.
	if lp, err := exec.LookPath(executablePath); err != nil {
		executablePath = lp
	}
}

func (sh *Shell) funcCmd(f *Func, args ...interface{}) (*Cmd, error) {
	// Safeguard against the developer forgetting to call InitMain, which could
	// lead to infinite recursion.
	if !calledInitMain {
		return nil, errDidNotCallInitMain
	}
	buf, err := encodeInvocation(f.handle, args...)
	if err != nil {
		return nil, err
	}
	vars := map[string]string{envInvocation: string(buf)}
	return sh.cmd(vars, executablePath)
}

func (sh *Shell) wait() error {
	// Note: It is illegal to call newCmdInternal concurrently with Shell.wait, so
	// we need not hold cleanupMu when accessing sh.cmds below.
	var res error
	for _, c := range sh.cmds {
		if !c.started || c.calledWait {
			continue
		}
		if err := c.wait(); !c.errorIsOk(err) {
			sh.logf("%s (PID %d) failed: %v\n", c.Path, c.Pid(), err)
			res = err
		}
	}
	return res
}

func copyFile(from, to string) error {
	fi, err := os.Stat(from)
	if err != nil {
		return err
	}
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fi.Mode().Perm())
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	cerr := out.Close()
	if err != nil {
		return err
	}
	return cerr
}

func (sh *Shell) move(oldpath, newpath string) error {
	var err error
	if err = os.Rename(oldpath, newpath); err != nil {
		// Concurrent, same-directory rename operations sometimes fail on certain
		// filesystems, so we retry once after a random backoff.
		time.Sleep(time.Duration(rand.Int63n(1000)) * time.Millisecond)
		err = os.Rename(oldpath, newpath)
	}
	// If the error was a LinkError, try copying the file over.
	if _, ok := err.(*os.LinkError); !ok {
		return err
	}
	if err := copyFile(oldpath, newpath); err != nil {
		return err
	}
	return os.Remove(oldpath)
}

func extractOutputFlag(flags ...string) (string, []string) {
	for i, f := range flags {
		if f == "-o" && len(flags) > i {
			return flags[i+1], append(flags[:i], flags[i+2:]...)
		}
	}
	return "", flags
}

func (sh *Shell) buildGoPkg(pkg string, flags ...string) (string, error) {
	outputFlag, flags := extractOutputFlag(flags...)
	binPath := filepath.Join(sh.Opts.BinDir, path.Base(pkg))
	if outputFlag != "" {
		if filepath.IsAbs(outputFlag) {
			binPath = outputFlag
		} else {
			binPath = filepath.Join(sh.Opts.BinDir, outputFlag)
		}
	}
	// If this binary has already been built, don't rebuild it.
	if _, err := os.Stat(binPath); err == nil {
		return binPath, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	// Build binary to tempBinPath (in a fresh temporary directory), then move it
	// to binPath.
	tempDir, err := ioutil.TempDir(sh.Opts.BinDir, "")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tempDir)
	tempBinPath := filepath.Join(tempDir, path.Base(pkg))
	args := []string{"build", "-o", tempBinPath}
	args = append(args, flags...)
	args = append(args, pkg)
	c, err := sh.cmd(nil, "go", args...)
	if err != nil {
		return "", err
	}
	if err := c.run(); err != nil {
		return "", err
	}
	if err := sh.move(tempBinPath, binPath); err != nil {
		return "", err
	}
	return binPath, nil
}

func (sh *Shell) makeTempFile() (*os.File, error) {
	sh.cleanupMu.Lock()
	defer sh.cleanupMu.Unlock()
	if sh.calledCleanup {
		return nil, errAlreadyCalledCleanup
	}
	f, err := ioutil.TempFile("", "")
	if err != nil {
		return nil, err
	}
	sh.tempFiles = append(sh.tempFiles, f)
	return f, nil
}

func (sh *Shell) makeTempDir() (string, error) {
	sh.cleanupMu.Lock()
	defer sh.cleanupMu.Unlock()
	if sh.calledCleanup {
		return "", errAlreadyCalledCleanup
	}
	name, err := ioutil.TempDir("", "")
	if err != nil {
		return "", err
	}
	sh.tempDirs = append(sh.tempDirs, name)
	return name, nil
}

func (sh *Shell) pushd(dir string) error {
	sh.cleanupMu.Lock()
	defer sh.cleanupMu.Unlock()
	if sh.calledCleanup {
		return errAlreadyCalledCleanup
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := os.Chdir(dir); err != nil {
		return err
	}
	sh.dirStack = append(sh.dirStack, cwd)
	return nil
}

func (sh *Shell) popd() error {
	sh.cleanupMu.Lock()
	defer sh.cleanupMu.Unlock()
	if sh.calledCleanup {
		return errAlreadyCalledCleanup
	}
	if len(sh.dirStack) == 0 {
		return errors.New("gosh: dir stack is empty")
	}
	dir := sh.dirStack[len(sh.dirStack)-1]
	if err := os.Chdir(dir); err != nil {
		return err
	}
	sh.dirStack = sh.dirStack[:len(sh.dirStack)-1]
	return nil
}

func (sh *Shell) addCleanupHandler(f func()) error {
	sh.cleanupMu.Lock()
	defer sh.cleanupMu.Unlock()
	if sh.calledCleanup {
		return errAlreadyCalledCleanup
	}
	sh.cleanupHandlers = append(sh.cleanupHandlers, f)
	return nil
}

// forEachRunningCmd applies f to each running child process.
func (sh *Shell) forEachRunningCmd(f func(*Cmd)) bool {
	anyRunning := false
	for _, c := range sh.cmds {
		if c.isRunning() {
			anyRunning = true
			if f != nil {
				f(c)
			}
		}
	}
	return anyRunning
}

// Note: It is safe to run Shell.terminateRunningCmds concurrently with the
// waiter goroutine and with Cmd.wait. In particular, Shell.terminateRunningCmds
// only calls c.{isRunning,Pid,signal}, all of which are thread-safe with the
// waiter goroutine and with Cmd.wait.
func (sh *Shell) terminateRunningCmds() {
	// Send os.Interrupt first; if that doesn't work, send os.Kill.
	anyRunning := sh.forEachRunningCmd(func(c *Cmd) {
		if err := c.signal(os.Interrupt); err != nil {
			sh.logf("%d.Signal(os.Interrupt) failed: %v\n", c.Pid(), err)
		}
	})
	// If any child is still running, wait for 100ms.
	if anyRunning {
		time.Sleep(100 * time.Millisecond)
		anyRunning = sh.forEachRunningCmd(func(c *Cmd) {
			sh.logf("%s (PID %d) did not die\n", c.Path, c.Pid())
		})
	}
	// If any child is still running, wait for another second, then send os.Kill
	// to all running children.
	if anyRunning {
		time.Sleep(time.Second)
		sh.forEachRunningCmd(func(c *Cmd) {
			if err := c.signal(os.Kill); err != nil {
				sh.logf("%d.Signal(os.Kill) failed: %v\n", c.Pid(), err)
			}
		})
		sh.logf("Killed all remaining child processes\n")
	}
}

func (sh *Shell) cleanup() {
	sh.calledCleanup = true
	// Terminate all children that are still running.
	sh.terminateRunningCmds()
	// Close and delete all temporary files.
	for _, tempFile := range sh.tempFiles {
		name := tempFile.Name()
		if err := tempFile.Close(); err != nil {
			sh.logf("%q.Close() failed: %v\n", name, err)
		}
		if err := os.RemoveAll(name); err != nil {
			sh.logf("os.RemoveAll(%q) failed: %v\n", name, err)
		}
	}
	// Delete all temporary directories.
	for _, tempDir := range sh.tempDirs {
		if err := os.RemoveAll(tempDir); err != nil {
			sh.logf("os.RemoveAll(%q) failed: %v\n", tempDir, err)
		}
	}
	// Change back to the top of the dir stack.
	if len(sh.dirStack) > 0 {
		dir := sh.dirStack[0]
		if err := os.Chdir(dir); err != nil {
			sh.logf("os.Chdir(%q) failed: %v\n", dir, err)
		}
	}
	// Call cleanup handlers in LIFO order.
	for i := len(sh.cleanupHandlers) - 1; i >= 0; i-- {
		sh.cleanupHandlers[i]()
	}
	close(sh.cleanupDone)
}

////////////////////////////////////////
// Public utilities

var calledInitMain = false

// InitMain must be called early on in main(), before flags are parsed. In the
// parent process, it returns immediately with no effect. In a child process for
// a Shell.FuncCmd command, it runs the specified function, then exits.
func InitMain() {
	calledInitMain = true
	s := os.Getenv(envInvocation)
	if s == "" {
		return
	}
	os.Unsetenv(envInvocation)
	InitChildMain()
	name, args, err := decodeInvocation(s)
	if err != nil {
		log.Fatal(err)
	}
	if err := callFunc(name, args...); err != nil {
		log.Fatal(err)
	}
	os.Exit(0)
}
