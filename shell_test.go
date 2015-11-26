package gosh_test

// TODO: Add more tests:
// - variadic function registration and invocation
// - shell cleanup
// - Cmd.{Wait,Run,CombinedOutput}
// - Shell.{AppendArgs,Wait,MakeTempFile,MakeTempDir}
// - ShellOpts (including defaulting behavior)
// - WatchParent

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/asadovsky/gosh"
	"github.com/asadovsky/gosh/example/lib"
)

func fatal(t *testing.T, args ...interface{}) {
	debug.PrintStack()
	t.Fatal(args...)
}

func fatalf(t *testing.T, format string, args ...interface{}) {
	debug.PrintStack()
	t.Fatalf(format, args...)
}

func ok(t *testing.T, err error) {
	if err != nil {
		fatal(t, err)
	}
}

func nok(t *testing.T, err error) {
	if err == nil {
		fatal(t, "nil err")
	}
}

func eq(t *testing.T, got, want interface{}) {
	if !reflect.DeepEqual(got, want) {
		fatalf(t, "got %v, want %v", got, want)
	}
}

func neq(t *testing.T, got, notWant interface{}) {
	if reflect.DeepEqual(got, notWant) {
		fatalf(t, "got %v", got)
	}
}

func env(sh *gosh.Shell) string {
	return strings.Join(sh.Env(), " ")
}

func TestEnv(t *testing.T) {
	sh := gosh.NewShell(gosh.ShellOpts{T: t})
	defer sh.Cleanup()
	eq(t, sh.Get("FOO"), "")
	eq(t, env(sh), "")
	sh.Set("FOO", "1")
	eq(t, sh.Get("FOO"), "1")
	eq(t, sh.Get("BAR"), "") // not in env
	eq(t, env(sh), "FOO=1")
	sh.Set("BAR", "2")
	eq(t, sh.Get("FOO"), "1")
	eq(t, sh.Get("BAR"), "2")
	eq(t, env(sh), "BAR=2 FOO=1")
	sh.SetMany("FOO=0")
	eq(t, env(sh), "BAR=2 FOO=0")
	sh.SetMany("FOO=3", "BAR=4", "BAZ=5")
	eq(t, env(sh), "BAR=4 BAZ=5 FOO=3")
	sh.SetMany("FOO=", "BAR=6", "BAZ=") // unset FOO and BAZ
	eq(t, env(sh), "BAR=6")
	sh.Unset("FOO")
	eq(t, env(sh), "BAR=6")
	sh.Unset("BAR")
	eq(t, env(sh), "")
}

func TestEnvSort(t *testing.T) {
	sh := gosh.NewShell(gosh.ShellOpts{T: t})
	defer sh.Cleanup()
	sh.Set("FOO4", "4")
	sh.Set("FOO", "bar")
	sh.Set("FOOD", "D")
	eq(t, env(sh), "FOO=bar FOO4=4 FOOD=D")
}

func TestPushdPopd(t *testing.T) {
	sh := gosh.NewShell(gosh.ShellOpts{T: t, NoDieOnErr: true})
	ok(t, sh.Err())
	defer sh.Cleanup()
	startDir, err := os.Getwd()
	ok(t, err)
	parentDir := filepath.Dir(startDir)
	neq(t, startDir, parentDir)
	sh.Pushd(parentDir)
	ok(t, sh.Err())
	cwd, err := os.Getwd()
	ok(t, err)
	eq(t, cwd, parentDir)
	sh.Pushd(startDir)
	ok(t, sh.Err())
	cwd, err = os.Getwd()
	ok(t, err)
	eq(t, cwd, startDir)
	sh.Popd()
	ok(t, sh.Err())
	cwd, err = os.Getwd()
	ok(t, err)
	eq(t, cwd, parentDir)
	sh.Popd()
	ok(t, sh.Err())
	cwd, err = os.Getwd()
	ok(t, err)
	eq(t, cwd, startDir)
	sh.Popd()
	nok(t, sh.Err())
}

func TestCmds(t *testing.T) {
	sh := gosh.NewShell(gosh.ShellOpts{T: t, SuppressChildOutput: true})
	defer sh.Cleanup()

	// Start server.
	binPath := sh.BuildGoPkg("github.com/asadovsky/gosh/example/server")
	c := sh.Cmd(nil, binPath)
	c.Start()
	c.AwaitReady()
	addr := c.AwaitVars("Addr")["Addr"]
	neq(t, addr, "")

	// Run client.
	binPath = sh.BuildGoPkg("github.com/asadovsky/gosh/example/client")
	c = sh.Cmd(nil, binPath, "-addr="+addr)
	output := string(c.Output())
	eq(t, output, "Hello, world!\n")
}

var (
	get   = gosh.Register("get", lib.Get)
	serve = gosh.Register("serve", lib.Serve)
)

func TestFns(t *testing.T) {
	sh := gosh.NewShell(gosh.ShellOpts{T: t, SuppressChildOutput: true})
	defer sh.Cleanup()

	// Start server.
	c := sh.Fn(nil, serve)
	c.Start()
	c.AwaitReady()
	addr := c.AwaitVars("Addr")["Addr"]
	neq(t, addr, "")

	// Run client.
	c = sh.Fn(nil, get, addr)
	output := string(c.Output())
	eq(t, output, "Hello, world!\n")
}

func TestShellMain(t *testing.T) {
	sh := gosh.NewShell(gosh.ShellOpts{T: t})
	defer sh.Cleanup()
	output := string(sh.Main(nil, lib.HelloWorldMain).Output())
	eq(t, output, "Hello, world!\n")
}

var write = gosh.Register("write", func(s string, stdout bool) error {
	var f *os.File
	if stdout {
		f = os.Stdout
	} else {
		f = os.Stderr
	}
	_, err := f.Write([]byte(s))
	return err
})

func toString(r io.Reader) string {
	buf := new(bytes.Buffer)
	buf.ReadFrom(r)
	return buf.String()
}

func TestStdoutStderr(t *testing.T) {
	sh := gosh.NewShell(gosh.ShellOpts{T: t})
	defer sh.Cleanup()
	s := "TestStdoutStderr\n"

	// Write to stdout.
	c := sh.Fn(nil, write, s, true)
	stdout, stderr := c.Stdout(), c.Stderr()
	output := string(c.CombinedOutput())
	eq(t, output, s)
	eq(t, toString(stdout), s)
	eq(t, toString(stderr), "")

	// Write to stderr.
	c = sh.Fn(nil, write, s, false)
	stdout, stderr = c.Stdout(), c.Stderr()
	output = string(c.CombinedOutput())
	eq(t, output, s)
	eq(t, toString(stdout), "")
	eq(t, toString(stderr), s)
}

var sleep = gosh.Register("sleep", func(d time.Duration) {
	time.Sleep(d)
})

func TestShutdown(t *testing.T) {
	sh := gosh.NewShell(gosh.ShellOpts{T: t})
	defer sh.Cleanup()

	for _, d := range []time.Duration{0, time.Second} {
		for _, s := range []syscall.Signal{syscall.SIGINT, syscall.SIGKILL} {
			fmt.Println(d, s)
			c := sh.Fn(nil, sleep, d)
			c.Start()
			time.Sleep(10 * time.Millisecond)
			c.Shutdown(s)
		}
	}
}

func TestMain(m *testing.M) {
	os.Exit(gosh.Run(m))
}
