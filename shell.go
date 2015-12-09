// Package gosh provides facilities for running and managing processes.
package gosh

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
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
	envInvocation     = "GOSH_INVOCATION"
)

var (
	errAlreadyCalledCleanup  = errors.New("already called cleanup")
	errNeedMaybeRunFnAndExit = errors.New("did not call MaybeRunFnAndExit")
)

// Shell represents a shell. Not thread-safe.
type Shell struct {
	// Err is the most recent error (may be nil).
	Err error
	// Opts is the ShellOpts for this Shell, with default values filled in.
	Opts ShellOpts
	// Vars is the map of env vars for this Shell.
	Vars map[string]string
	// Args is the list of args to append to subsequent command invocations.
	Args []string
	// Internal state.
	cmds          []*Cmd
	tempFiles     []*os.File
	tempDirs      []string
	dirStack      []string // for pushd/popd
	cleanupFns    []func()
	cleanupMu     sync.Mutex // protects calledCleanup
	calledCleanup bool
}

// ShellOpts configures Shell.
type ShellOpts struct {
	// OnError is called whenever an error is encountered.
	// If not specified, defaults to panic(err).
	OnError func(err error)
	// OnLog is called to log things.
	// If not specified, defaults to log.Println(args...).
	OnLog func(args ...interface{})
	// Child stdout and stderr are propagated up to the parent's stdout and stderr
	// iff SuppressChildOutput is false.
	SuppressChildOutput bool
	// If specified, each child's stdout and stderr streams are also piped to
	// files in this directory.
	// If not specified, defaults to GOSH_CHILD_OUTPUT_DIR.
	ChildOutputDir string
	// Directory where BuildGoPkg() writes compiled binaries.
	// If not specified, defaults to GOSH_BIN_DIR.
	BinDir string
}

// NewShell returns a new Shell.
func NewShell(opts ShellOpts) *Shell {
	sh, err := newShell(opts)
	sh.SetErr(err)
	return sh
}

// SetErr sets sh.Err. If err is not nil, it also calls sh.Opts.OnError.
func (sh *Shell) SetErr(err error) {
	sh.Err = err
	if err != nil && sh.Opts.OnError != nil {
		sh.Opts.OnError(err)
	}
}

// Cmd returns a Cmd for an invocation of the named program.
func (sh *Shell) Cmd(name string, args ...string) *Cmd {
	sh.ok()
	res, err := sh.cmd(nil, name, args...)
	sh.SetErr(err)
	return res
}

// Fn returns a Cmd for an invocation of the given registered Fn.
func (sh *Shell) Fn(fn *Fn, args ...interface{}) *Cmd {
	sh.ok()
	res, err := sh.fn(fn, args...)
	sh.SetErr(err)
	return res
}

// Main returns a Cmd for an invocation of the given registered main() function.
// Intended usage: Have your program's main() call RealMain, then write a parent
// program that uses Shell.Main to run RealMain in a child process. With this
// approach, RealMain can be compiled into the parent program's binary. Caveat:
// potential flag collisions.
func (sh *Shell) Main(fn *Fn, args ...string) *Cmd {
	sh.ok()
	res, err := sh.main(fn, args...)
	sh.SetErr(err)
	return res
}

// Get returns the value of the given env var.
func (sh *Shell) Get(key string) string {
	sh.ok()
	return sh.get(key)
}

// Set sets the given env var.
func (sh *Shell) Set(key, value string) {
	sh.ok()
	sh.set(key, value)
}

// Unset unsets the given env var.
func (sh *Shell) Unset(key string) {
	sh.ok()
	sh.unset(key)
}

// SetMany sets the given env vars, of the form "key=value" or "key=".
func (sh *Shell) SetMany(vars ...string) {
	sh.ok()
	sh.setMany(vars...)
}

// Wait waits for all commands started by this Shell to exit.
func (sh *Shell) Wait() {
	sh.ok()
	sh.SetErr(sh.wait())
}

// BuildGoPkg compiles a Go package using the "go build" command and writes the
// resulting binary to sh.Opts.BinDir. Returns the absolute path to the binary.
// Included in Shell for convenience, but could have just as easily been
// provided as a utility function.
func (sh *Shell) BuildGoPkg(pkg string, flags ...string) string {
	sh.ok()
	res, err := sh.buildGoPkg(pkg, flags...)
	sh.SetErr(err)
	return res
}

// MakeTempFile creates a new temporary file in os.TempDir, opens the file for
// reading and writing, and returns the resulting *os.File.
func (sh *Shell) MakeTempFile() *os.File {
	sh.ok()
	res, err := sh.makeTempFile()
	sh.SetErr(err)
	return res
}

// MakeTempDir creates a new temporary directory in os.TempDir and returns the
// path of the new directory.
func (sh *Shell) MakeTempDir() string {
	sh.ok()
	res, err := sh.makeTempDir()
	sh.SetErr(err)
	return res
}

// Pushd behaves like Bash pushd.
func (sh *Shell) Pushd(dir string) {
	sh.ok()
	sh.SetErr(sh.pushd(dir))
}

// Popd behaves like Bash popd.
func (sh *Shell) Popd() {
	sh.ok()
	sh.SetErr(sh.popd())
}

// Cleanup cleans up all resources (child processes, temporary files and
// directories) associated with this Shell.
func (sh *Shell) Cleanup() {
	sh.cleanupMu.Lock()
	if sh.calledCleanup {
		sh.cleanupMu.Unlock()
		panic(errAlreadyCalledCleanup)
	}
	sh.calledCleanup = true
	sh.cleanupMu.Unlock()
	sh.cleanup()
}

// AddToCleanup registers the given function to be called by Shell.Cleanup().
func (sh *Shell) AddToCleanup(fn func()) {
	sh.ok()
	sh.cleanupFns = append(sh.cleanupFns, fn)
}

////////////////////////////////////////
// Internals

// onTerminationSignal starts a goroutine that listens for various termination
// signals and calls the given function when such a signal is received.
func onTerminationSignal(fn func(os.Signal)) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)
	go func() {
		fn(<-ch)
	}()
}

func newShell(opts ShellOpts) (*Shell, error) {
	if opts.OnError == nil {
		opts.OnError = func(err error) { panic(err) }
	}
	if opts.OnLog == nil {
		opts.OnLog = func(args ...interface{}) { log.Println(args...) }
	}
	if opts.ChildOutputDir == "" {
		opts.ChildOutputDir = os.Getenv(envChildOutputDir)
	}
	sh := &Shell{
		Opts:       opts,
		Vars:       map[string]string{},
		Args:       []string{},
		cmds:       []*Cmd{},
		tempFiles:  []*os.File{},
		tempDirs:   []string{},
		dirStack:   []string{},
		cleanupFns: []func(){},
	}
	if sh.Opts.BinDir == "" {
		sh.Opts.BinDir = os.Getenv(envBinDir)
		if sh.Opts.BinDir == "" {
			var err error
			if sh.Opts.BinDir, err = sh.makeTempDir(); err != nil {
				return sh, err // NewShell will call sh.SetErr
			}
		}
	}
	// Set this process's PGID to its PID so that its child processes can be
	// identified reliably.
	// http://man7.org/linux/man-pages/man2/setpgid.2.html
	// TODO: Is there any way to reliably kill all spawned subprocesses without
	// modifying external state?
	if err := syscall.Setpgid(0, 0); err != nil {
		return sh, err // NewShell will call sh.SetErr
	}
	// Call sh.cleanup() if needed when a termination signal is received.
	onTerminationSignal(func(sig os.Signal) {
		sh.warningf("Received signal: %v", sig)
		sh.cleanupMu.Lock()
		if !sh.calledCleanup {
			sh.calledCleanup = true
			sh.cleanupMu.Unlock()
			sh.cleanup()
		} else {
			sh.cleanupMu.Unlock()
		}
		if s, ok := sig.(syscall.Signal); !ok {
			os.Exit(1)
		} else {
			os.Exit(int(s))
		}
	})
	return sh, nil
}

func (sh *Shell) log(args ...interface{}) {
	if sh.Opts.OnLog != nil {
		sh.Opts.OnLog(args...)
	}
}

func (sh *Shell) warningf(format string, args ...interface{}) {
	sh.log(fmt.Sprintf("WARNING: "+format, args...))
}

func (sh *Shell) errorf(format string, args ...interface{}) {
	sh.log(fmt.Sprintf("ERROR: "+format, args...))
}

func (sh *Shell) ok() {
	sh.cleanupMu.Lock()
	defer sh.cleanupMu.Unlock()
	if sh.calledCleanup {
		panic(errAlreadyCalledCleanup)
	}
}

func (sh *Shell) cmd(vars map[string]string, name string, args ...string) (*Cmd, error) {
	opts := CmdOpts{
		SuppressOutput: sh.Opts.SuppressChildOutput,
		OutputDir:      sh.Opts.ChildOutputDir,
	}
	vars = mergeMaps(sliceToMap(os.Environ()), sh.Vars, vars)
	return newCmd(sh, opts, vars, name, append(args, sh.Args...)...)
}

func (sh *Shell) fn(fn *Fn, args ...interface{}) (*Cmd, error) {
	// Safeguard against the developer forgetting to call MaybeRunFnAndExit, which
	// could lead to infinite recursion.
	if !calledMaybeRunFnAndExit {
		return nil, errNeedMaybeRunFnAndExit
	}
	b, err := encInvocation(fn.name, args...)
	if err != nil {
		return nil, err
	}
	vars := map[string]string{envInvocation: string(b)}
	return sh.cmd(vars, os.Args[0])
}

func (sh *Shell) main(fn *Fn, args ...string) (*Cmd, error) {
	// Safeguard against the developer forgetting to call MaybeRunFnAndExit, which
	// could lead to infinite recursion.
	if !calledMaybeRunFnAndExit {
		return nil, errNeedMaybeRunFnAndExit
	}
	// Check that fn has the required signature.
	t := fn.value.Type()
	if t.NumIn() != 0 || t.NumOut() != 0 {
		return nil, errors.New("main function must have no input or output parameters")
	}
	b, err := encInvocation(fn.name)
	if err != nil {
		return nil, err
	}
	vars := map[string]string{envInvocation: string(b)}
	return sh.cmd(vars, os.Args[0], args...)
}

func (sh *Shell) get(key string) string {
	return sh.Vars[key]
}

func (sh *Shell) set(key, value string) {
	sh.Vars[key] = value
}

func (sh *Shell) setMany(vars ...string) {
	for _, kv := range vars {
		k, v := splitKeyValue(kv)
		if v == "" {
			delete(sh.Vars, k)
		} else {
			sh.Vars[k] = v
		}
	}
}

func (sh *Shell) unset(key string) {
	delete(sh.Vars, key)
}

func (sh *Shell) wait() error {
	var res error
	for _, c := range sh.cmds {
		if !c.calledStart() || c.calledWait {
			continue
		}
		if err := c.wait(); err != nil {
			sh.errorf("Cmd.Wait() failed: %v", err)
			if res == nil {
				res = err
			}
		}
	}
	return res
}

func (sh *Shell) buildGoPkg(pkg string, flags ...string) (string, error) {
	binPath := filepath.Join(sh.Opts.BinDir, path.Base(pkg))
	// If this binary has already been built, don't rebuild it.
	if _, err := os.Stat(binPath); err == nil {
		return binPath, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	// Build binary to tempBinPath, then move it to binPath.
	tempDir, err := ioutil.TempDir(sh.Opts.BinDir, "")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tempDir)
	tempBinPath := filepath.Join(tempDir, path.Base(pkg))
	args := []string{"build", "-x", "-o", tempBinPath}
	args = append(args, flags...)
	args = append(args, pkg)
	c, err := sh.cmd(nil, "go", args...)
	if err != nil {
		return "", err
	}
	c.Opts.SuppressOutput = true
	if err := c.run(); err != nil {
		return "", err
	}
	if err := os.Rename(tempBinPath, binPath); err != nil {
		return "", err
	}
	return binPath, nil
}

func (sh *Shell) makeTempFile() (*os.File, error) {
	f, err := ioutil.TempFile("", "")
	if err != nil {
		return nil, err
	}
	sh.tempFiles = append(sh.tempFiles, f)
	return f, nil
}

func (sh *Shell) makeTempDir() (string, error) {
	name, err := ioutil.TempDir("", "")
	if err != nil {
		return "", err
	}
	sh.tempDirs = append(sh.tempDirs, name)
	return name, nil
}

func (sh *Shell) pushd(dir string) error {
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
	if len(sh.dirStack) == 0 {
		return errors.New("dir stack is empty")
	}
	dir := sh.dirStack[len(sh.dirStack)-1]
	if err := os.Chdir(dir); err != nil {
		return err
	}
	sh.dirStack = sh.dirStack[:len(sh.dirStack)-1]
	return nil
}

// forEachRunningCmd applies fn to each running child process.
func (sh *Shell) forEachRunningCmd(fn func(*Cmd)) bool {
	anyRunning := false
	for _, c := range sh.cmds {
		if !c.calledStart() || c.c.Process == nil {
			continue // not started
		}
		if pgid, err := syscall.Getpgid(c.c.Process.Pid); err != nil || pgid != os.Getpid() {
			continue // not our child
		}
		anyRunning = true
		fn(c)
	}
	return anyRunning
}

func (sh *Shell) cleanup() {
	// Note, newShell() calls syscall.Setpgid().
	if os.Getpid() != syscall.Getpgrp() {
		panic(fmt.Sprint(os.Getpid(), syscall.Getpgrp()))
	}
	// Terminate all children that are still running. Try SIGINT first; if that
	// doesn't work, use SIGKILL.
	// https://golang.org/pkg/os/#Process.Signal
	anyRunning := sh.forEachRunningCmd(func(c *Cmd) {
		if err := c.c.Process.Signal(os.Interrupt); err != nil {
			sh.warningf("%d.Signal(SIGINT) failed: %v", c.c.Process.Pid, err)
		}
	})
	// If any child is still running, wait for 50ms.
	if anyRunning {
		time.Sleep(50 * time.Millisecond)
		anyRunning = sh.forEachRunningCmd(func(c *Cmd) {
			sh.warningf("%s (PID %d) did not die", c.c.Path, c.c.Process.Pid)
		})
	}
	// If any child is still running, wait for another second, then send SIGKILL
	// to all running children.
	if anyRunning {
		time.Sleep(time.Second)
		sh.warningf("sending SIGKILL to all remaining child processes")
		sh.forEachRunningCmd(func(c *Cmd) {
			if err := c.c.Process.Kill(); err != nil {
				sh.warningf("%d.Kill() failed: %v", c.c.Process.Pid, err)
			}
		})
	}
	// Close and delete all temporary files.
	for _, tempFile := range sh.tempFiles {
		name := tempFile.Name()
		if err := tempFile.Close(); err != nil {
			sh.warningf("%q.Close() failed: %v", name, err)
		}
		if err := os.RemoveAll(name); err != nil {
			sh.warningf("os.RemoveAll(%q) failed: %v", name, err)
		}
	}
	// Delete all temporary directories.
	for _, tempDir := range sh.tempDirs {
		if err := os.RemoveAll(tempDir); err != nil {
			sh.warningf("os.RemoveAll(%q) failed: %v", tempDir, err)
		}
	}
	// Call any registered cleanup functions in LIFO order.
	for i := len(sh.cleanupFns) - 1; i >= 0; i-- {
		sh.cleanupFns[i]()
	}
}

////////////////////////////////////////
// Public utilities

var calledMaybeRunFnAndExit = false

// MaybeRunFnAndExit must be called first thing in main() or TestMain(), before
// flags are parsed. In the parent process, it returns immediately with no
// effect. In a child process for a Shell.Fn() or Shell.Main() command, it runs
// the specified function, then exits.
func MaybeRunFnAndExit() {
	calledMaybeRunFnAndExit = true
	s := os.Getenv(envInvocation)
	if s == "" {
		return
	}
	WatchParent()
	if name, args, err := decInvocation(s); err != nil {
		log.Fatal(err)
	} else {
		if err := Call(name, args...); err != nil {
			log.Fatal(err)
		}
		os.Exit(0)
	}
}

// Run calls MaybeRunFnAndExit(), then returns run(). Exported so that TestMain
// functions can simply call os.Exit(gosh.Run(m.Run)).
func Run(run func() int) int {
	MaybeRunFnAndExit()
	return run()
}
