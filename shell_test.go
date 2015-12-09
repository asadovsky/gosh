package gosh_test

// TODO: Add more tests:
// - variadic function registration and invocation
// - shell cleanup
// - Cmd.{Wait,Run,CombinedOutput}
// - Shell.{Args,Wait,MakeTempFile,MakeTempDir}
// - ShellOpts (including defaulting behavior)
// - WatchParent

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"strings"
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

func fatalOnError(t *testing.T) func(err error) {
	return func(err error) {
		fatal(t, err)
	}
}

// Mimics env_util.go sliceToMap.
func vars(s string) map[string]string {
	m := make(map[string]string)
	if s == "" {
		return m
	}
	for _, kv := range strings.Split(s, " ") {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			panic(kv)
		}
		m[parts[0]] = parts[1]
	}
	return m
}

func TestVars(t *testing.T) {
	sh := gosh.NewShell(gosh.ShellOpts{OnError: fatalOnError(t)})
	defer sh.Cleanup()
	eq(t, sh.Get("FOO"), "")
	eq(t, sh.Vars, vars(""))
	sh.Set("FOO", "1")
	eq(t, sh.Get("FOO"), "1")
	eq(t, sh.Get("BAR"), "")
	eq(t, sh.Vars, vars("FOO=1"))
	sh.Set("BAR", "2")
	eq(t, sh.Get("FOO"), "1")
	eq(t, sh.Get("BAR"), "2")
	eq(t, sh.Vars, vars("BAR=2 FOO=1"))
	sh.SetMany("FOO=0")
	eq(t, sh.Vars, vars("BAR=2 FOO=0"))
	sh.SetMany("FOO=3", "BAR=4", "BAZ=5")
	eq(t, sh.Vars, vars("BAR=4 BAZ=5 FOO=3"))
	sh.SetMany("FOO=", "BAR=6", "BAZ=")
	eq(t, sh.Vars, vars("BAR=6"))
	sh.Unset("FOO")
	eq(t, sh.Vars, vars("BAR=6"))
	sh.Unset("BAR")
	eq(t, sh.Vars, vars(""))
}

func TestPushdPopd(t *testing.T) {
	sh := gosh.NewShell(gosh.ShellOpts{OnError: fatalOnError(t)})
	defer sh.Cleanup()
	startDir, err := os.Getwd()
	ok(t, err)
	parentDir := filepath.Dir(startDir)
	neq(t, startDir, parentDir)
	sh.Pushd(parentDir)
	cwd, err := os.Getwd()
	ok(t, err)
	eq(t, cwd, parentDir)
	sh.Pushd(startDir)
	cwd, err = os.Getwd()
	ok(t, err)
	eq(t, cwd, startDir)
	sh.Popd()
	cwd, err = os.Getwd()
	ok(t, err)
	eq(t, cwd, parentDir)
	sh.Popd()
	cwd, err = os.Getwd()
	ok(t, err)
	eq(t, cwd, startDir)
	// The next sh.Popd() will fail.
	var gotErr error
	sh.Opts.OnError = func(err error) { gotErr = err }
	sh.Popd()
	nok(t, sh.Err)
	eq(t, gotErr, sh.Err)
}

func TestCmds(t *testing.T) {
	sh := gosh.NewShell(gosh.ShellOpts{OnError: fatalOnError(t)})
	defer sh.Cleanup()

	// Start server.
	binPath := sh.BuildGoPkg("github.com/asadovsky/gosh/example/server")
	c := sh.Cmd(binPath)
	c.Start()
	c.AwaitReady()
	addr := c.AwaitVars("Addr")["Addr"]
	neq(t, addr, "")

	// Run client.
	binPath = sh.BuildGoPkg("github.com/asadovsky/gosh/example/client")
	c = sh.Cmd(binPath, "-addr="+addr)
	stdout, _ := c.Output()
	eq(t, string(stdout), "Hello, world!\n")
}

var (
	get   = gosh.Register("get", lib.Get)
	serve = gosh.Register("serve", lib.Serve)
)

func TestFns(t *testing.T) {
	sh := gosh.NewShell(gosh.ShellOpts{OnError: fatalOnError(t)})
	defer sh.Cleanup()

	// Start server.
	c := sh.Fn(serve)
	c.Start()
	c.AwaitReady()
	addr := c.AwaitVars("Addr")["Addr"]
	neq(t, addr, "")

	// Run client.
	c = sh.Fn(get, addr)
	stdout, _ := c.Output()
	eq(t, string(stdout), "Hello, world!\n")
}

func TestShellMain(t *testing.T) {
	sh := gosh.NewShell(gosh.ShellOpts{OnError: fatalOnError(t)})
	defer sh.Cleanup()
	stdout, _ := sh.Main(lib.HelloWorldMain).Output()
	eq(t, string(stdout), "Hello, world!\n")
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
	if b, err := ioutil.ReadAll(r); err != nil {
		panic(err)
	} else {
		return string(b)
	}
}

func TestStdoutStderr(t *testing.T) {
	sh := gosh.NewShell(gosh.ShellOpts{OnError: fatalOnError(t)})
	defer sh.Cleanup()
	s := "TestStdoutStderr\n"

	// Write to stdout.
	c := sh.Fn(write, s, true)
	stdout, stderr := c.Stdout(), c.Stderr()
	output := string(c.CombinedOutput())
	eq(t, output, s)
	eq(t, toString(stdout), s)
	eq(t, toString(stderr), "")

	// Write to stderr.
	c = sh.Fn(write, s, false)
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
	sh := gosh.NewShell(gosh.ShellOpts{OnError: fatalOnError(t)})
	defer sh.Cleanup()

	for _, d := range []time.Duration{0, time.Second} {
		for _, s := range []os.Signal{os.Interrupt, os.Kill} {
			fmt.Println(d, s)
			c := sh.Fn(sleep, d)
			c.Start()
			time.Sleep(10 * time.Millisecond)
			c.Shutdown(s)
		}
	}
}

func TestMain(m *testing.M) {
	os.Exit(gosh.Run(m.Run))
}
