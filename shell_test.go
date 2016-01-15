// Copyright 2015 The Vanadium Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gosh_test

// TODO(sadovsky): Add more tests:
// - effects of Shell.Cleanup
// - Shell.{Vars,Args,Rename,MakeTempFile,MakeTempDir}
// - Shell.Opts.{PropagateChildOutput,ChildOutputDir,BinDir}
// - Cmd.Clone
// - Cmd.Opts.{IgnoreParentExit,ExitAfter,PropagateOutput}

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"testing"
	"time"

	"github.com/asadovsky/gosh"
	"github.com/asadovsky/gosh/internal/gosh_example_lib"
)

var fakeError = errors.New("fake error")

func fatal(t *testing.T, v ...interface{}) {
	debug.PrintStack()
	t.Fatal(v...)
}

func fatalf(t *testing.T, format string, v ...interface{}) {
	debug.PrintStack()
	t.Fatalf(format, v...)
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

func toString(t *testing.T, r io.Reader) string {
	b, err := ioutil.ReadAll(r)
	ok(t, err)
	return string(b)
}

func makeFatalf(t *testing.T) func(string, ...interface{}) {
	return func(format string, v ...interface{}) {
		debug.PrintStack()
		t.Fatalf(format, v...)
	}
}

func setsErr(t *testing.T, sh *gosh.Shell, f func()) {
	calledFatalf := false
	sh.Opts.Fatalf = func(string, ...interface{}) { calledFatalf = true }
	f()
	nok(t, sh.Err)
	eq(t, calledFatalf, true)
	sh.Err = nil
	sh.Opts.Fatalf = makeFatalf(t)
}

////////////////////////////////////////
// Simple functions

// Simplified versions of various Unix commands.
var (
	catFunc = gosh.RegisterFunc("catFunc", func() {
		io.Copy(os.Stdout, os.Stdin)
	})
	echoFunc = gosh.RegisterFunc("echoFunc", func() {
		fmt.Println(os.Args[1])
	})
	readFunc = gosh.RegisterFunc("readFunc", func() {
		bufio.NewReader(os.Stdin).ReadString('\n')
	})
)

// Functions with parameters.
var (
	exitFunc = gosh.RegisterFunc("exitFunc", func(code int) {
		os.Exit(code)
	})
	sleepFunc = gosh.RegisterFunc("sleepFunc", func(d time.Duration, code int) {
		// For TestSignal and TestTerminate.
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt)
		go func() {
			<-ch
			os.Exit(0)
		}()
		time.Sleep(d)
		os.Exit(code)
	})
	printFunc = gosh.RegisterFunc("printFunc", func(v ...interface{}) {
		fmt.Print(v...)
	})
	printfFunc = gosh.RegisterFunc("printfFunc", func(format string, v ...interface{}) {
		fmt.Printf(format, v...)
	})
)

////////////////////////////////////////
// Tests

func TestCustomFatalf(t *testing.T) {
	sh := gosh.NewShell(gosh.Opts{Fatalf: makeFatalf(t), Logf: t.Logf})
	defer sh.Cleanup()

	var calledFatalf bool
	sh.Opts.Fatalf = func(string, ...interface{}) { calledFatalf = true }
	sh.HandleError(fakeError)
	// Note, our deferred sh.Cleanup() should succeed despite this error.
	nok(t, sh.Err)
	eq(t, calledFatalf, true)
}

func TestPushdPopd(t *testing.T) {
	sh := gosh.NewShell(gosh.Opts{Fatalf: makeFatalf(t), Logf: t.Logf})
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
	setsErr(t, sh, func() { sh.Popd() })
}

func evalSymlinks(t *testing.T, dir string) string {
	var err error
	dir, err = filepath.EvalSymlinks(dir)
	ok(t, err)
	return dir
}

func getwdEvalSymlinks(t *testing.T) string {
	dir, err := os.Getwd()
	ok(t, err)
	return evalSymlinks(t, dir)
}

func TestPushdNoPopdCleanup(t *testing.T) {
	startDir := getwdEvalSymlinks(t)
	sh := gosh.NewShell(gosh.Opts{Fatalf: makeFatalf(t), Logf: t.Logf})
	tmpDir := sh.MakeTempDir()
	sh.Pushd(tmpDir)
	eq(t, getwdEvalSymlinks(t), evalSymlinks(t, tmpDir))
	// There is no matching popd; the cwd is tmpDir, which is deleted by Cleanup.
	// Cleanup needs to put us back in startDir, otherwise all subsequent Pushd
	// calls will fail.
	sh.Cleanup()
	eq(t, getwdEvalSymlinks(t), startDir)
}

func TestCmd(t *testing.T) {
	sh := gosh.NewShell(gosh.Opts{Fatalf: makeFatalf(t), Logf: t.Logf})
	defer sh.Cleanup()

	// Start server.
	binPath := sh.BuildGoPkg("github.com/asadovsky/gosh/internal/gosh_example_server")
	c := sh.Cmd(binPath)
	c.Start()
	c.AwaitReady()
	addr := c.AwaitVars("Addr")["Addr"]
	neq(t, addr, "")

	// Run client.
	binPath = sh.BuildGoPkg("github.com/asadovsky/gosh/internal/gosh_example_client")
	c = sh.Cmd(binPath, "-addr="+addr)
	eq(t, c.Stdout(), "Hello, world!\n")
}

var (
	getFunc   = gosh.RegisterFunc("getFunc", lib.Get)
	serveFunc = gosh.RegisterFunc("serveFunc", lib.Serve)
)

func TestFuncCmd(t *testing.T) {
	sh := gosh.NewShell(gosh.Opts{Fatalf: makeFatalf(t), Logf: t.Logf})
	defer sh.Cleanup()

	// Start server.
	c := sh.FuncCmd(serveFunc)
	c.Start()
	c.AwaitReady()
	addr := c.AwaitVars("Addr")["Addr"]
	neq(t, addr, "")

	// Run client.
	c = sh.FuncCmd(getFunc, addr)
	eq(t, c.Stdout(), "Hello, world!\n")
}

// Functions designed for TestRegistry.
var (
	printIntsFunc = gosh.RegisterFunc("printIntsFunc", func(v ...int) {
		var vi []interface{}
		for _, x := range v {
			vi = append(vi, x)
		}
		fmt.Print(vi...)
	})
	printfIntsFunc = gosh.RegisterFunc("printfIntsFunc", func(format string, v ...int) {
		var vi []interface{}
		for _, x := range v {
			vi = append(vi, x)
		}
		fmt.Printf(format, vi...)
	})
)

// Tests that Await{Ready,Vars} return immediately when the process exits.
func TestAwaitProcessExit(t *testing.T) {
	sh := gosh.NewShell(gosh.Opts{Fatalf: makeFatalf(t), Logf: t.Logf})
	defer sh.Cleanup()

	c := sh.FuncCmd(exitFunc, 0)
	c.Start()
	setsErr(t, sh, func() { c.AwaitReady() })

	c = sh.FuncCmd(exitFunc, 0)
	c.Start()
	setsErr(t, sh, func() { c.AwaitVars("foo") })
}

// Tests function signature-checking and execution.
func TestRegistry(t *testing.T) {
	sh := gosh.NewShell(gosh.Opts{Fatalf: makeFatalf(t), Logf: t.Logf})
	defer sh.Cleanup()

	// Variadic functions. Non-variadic functions are sufficiently covered in
	// other tests.
	eq(t, sh.FuncCmd(printFunc).Stdout(), "")
	eq(t, sh.FuncCmd(printFunc, 0).Stdout(), "0")
	eq(t, sh.FuncCmd(printFunc, 0, "foo").Stdout(), "0foo")
	eq(t, sh.FuncCmd(printfFunc, "").Stdout(), "")
	eq(t, sh.FuncCmd(printfFunc, "%v", 0).Stdout(), "0")
	eq(t, sh.FuncCmd(printfFunc, "%v%v", 0, "foo").Stdout(), "0foo")
	eq(t, sh.FuncCmd(printIntsFunc, 1, 2).Stdout(), "1 2")
	eq(t, sh.FuncCmd(printfIntsFunc, "%v %v", 1, 2).Stdout(), "1 2")

	// Too few arguments.
	setsErr(t, sh, func() { sh.FuncCmd(exitFunc) })
	setsErr(t, sh, func() { sh.FuncCmd(sleepFunc, time.Second) })
	setsErr(t, sh, func() { sh.FuncCmd(printfFunc) })

	// Too many arguments.
	setsErr(t, sh, func() { sh.FuncCmd(exitFunc, 0, 0) })
	setsErr(t, sh, func() { sh.FuncCmd(sleepFunc, time.Second, 0, 0) })

	// Wrong argument types.
	setsErr(t, sh, func() { sh.FuncCmd(exitFunc, "foo") })
	setsErr(t, sh, func() { sh.FuncCmd(sleepFunc, 0, 0) })
	setsErr(t, sh, func() { sh.FuncCmd(printfFunc, 0) })
	setsErr(t, sh, func() { sh.FuncCmd(printfFunc, 0, 0) })

	// Wrong variadic argument types.
	setsErr(t, sh, func() { sh.FuncCmd(printIntsFunc, 0.5) })
	setsErr(t, sh, func() { sh.FuncCmd(printIntsFunc, 0, 0.5) })
	setsErr(t, sh, func() { sh.FuncCmd(printfIntsFunc, "%v", 0.5) })
	setsErr(t, sh, func() { sh.FuncCmd(printfIntsFunc, "%v", 0, 0.5) })

	// Unsupported argument types.
	var p *int
	setsErr(t, sh, func() { sh.FuncCmd(printFunc, p) })
	setsErr(t, sh, func() { sh.FuncCmd(printfFunc, "%v", p) })
}

func TestStdin(t *testing.T) {
	sh := gosh.NewShell(gosh.Opts{Fatalf: makeFatalf(t), Logf: t.Logf})
	defer sh.Cleanup()

	c := sh.FuncCmd(catFunc)
	c.Stdin = "foo\n"
	// We set c.Stdin and did not call c.StdinPipe(), so stdin should close and
	// cat should exit immediately.
	eq(t, c.Stdout(), "foo\n")

	c = sh.FuncCmd(catFunc)
	c.StdinPipe().Write([]byte("foo\n"))
	// The "cat" command only exits when stdin is closed, so we must explicitly
	// close the stdin pipe. Note, it's safe to call c.StdinPipe multiple times.
	c.StdinPipe().Close()
	eq(t, c.Stdout(), "foo\n")

	c = sh.FuncCmd(readFunc)
	c.StdinPipe().Write([]byte("foo\n"))
	// The "read" command exits when it sees a newline, so Cmd.Wait (and thus
	// Cmd.Run) should return immediately; it should not be necessary to close the
	// stdin pipe.
	c.Run()

	c = sh.FuncCmd(catFunc)
	// No stdin, so cat should exit immediately.
	eq(t, c.Stdout(), "")

	// It's an error (detected at command start time) to both set c.Stdin and call
	// c.StdinPipe. Note, this indirectly tests that Shell.Cleanup works even if
	// some Cmd.Start failed.
	c = sh.FuncCmd(catFunc)
	c.Stdin = "foo"
	c.StdinPipe().Write([]byte("bar"))
	c.StdinPipe().Close()
	setsErr(t, sh, func() { c.Start() })
}

var writeFunc = gosh.RegisterFunc("writeFunc", func(stdout, stderr bool) error {
	if stdout {
		if _, err := os.Stdout.Write([]byte("A")); err != nil {
			return err
		}
	}
	if stderr {
		if _, err := os.Stderr.Write([]byte("B")); err != nil {
			return err
		}
	}
	if stdout {
		if _, err := os.Stdout.Write([]byte("A")); err != nil {
			return err
		}
	}
	if stderr {
		if _, err := os.Stderr.Write([]byte("B")); err != nil {
			return err
		}
	}
	return nil
})

func TestStdoutStderr(t *testing.T) {
	sh := gosh.NewShell(gosh.Opts{Fatalf: makeFatalf(t), Logf: t.Logf})
	defer sh.Cleanup()

	// Write to stdout only.
	c := sh.FuncCmd(writeFunc, true, false)
	stdoutPipe, stderrPipe := c.StdoutPipe(), c.StderrPipe()
	stdout, stderr := c.StdoutStderr()
	eq(t, stdout, "AA")
	eq(t, stderr, "")
	eq(t, toString(t, stdoutPipe), "AA")
	eq(t, toString(t, stderrPipe), "")

	// Write to stderr only.
	c = sh.FuncCmd(writeFunc, false, true)
	stdoutPipe, stderrPipe = c.StdoutPipe(), c.StderrPipe()
	stdout, stderr = c.StdoutStderr()
	eq(t, stdout, "")
	eq(t, stderr, "BB")
	eq(t, toString(t, stdoutPipe), "")
	eq(t, toString(t, stderrPipe), "BB")

	// Write to both stdout and stderr.
	c = sh.FuncCmd(writeFunc, true, true)
	stdoutPipe, stderrPipe = c.StdoutPipe(), c.StderrPipe()
	stdout, stderr = c.StdoutStderr()
	eq(t, stdout, "AA")
	eq(t, stderr, "BB")
	eq(t, toString(t, stdoutPipe), "AA")
	eq(t, toString(t, stderrPipe), "BB")
}

var writeMoreFunc = gosh.RegisterFunc("writeMoreFunc", func() {
	sh := gosh.NewShell(gosh.Opts{})
	defer sh.Cleanup()

	c := sh.FuncCmd(writeFunc, true, true)
	c.AddStdoutWriter(gosh.NopWriteCloser(os.Stdout))
	c.AddStderrWriter(gosh.NopWriteCloser(os.Stderr))
	c.Run()

	fmt.Fprint(os.Stdout, " stdout done")
	fmt.Fprint(os.Stderr, " stderr done")
})

// Tests that it's safe to add wrapped os.Stdout and os.Stderr as writers.
func TestAddWritersWrappedStdoutStderr(t *testing.T) {
	sh := gosh.NewShell(gosh.Opts{Fatalf: makeFatalf(t), Logf: t.Logf})
	defer sh.Cleanup()

	stdout, stderr := sh.FuncCmd(writeMoreFunc).StdoutStderr()
	eq(t, stdout, "AA stdout done")
	eq(t, stderr, "BB stderr done")
}

// Tests that adding non-wrapped os.Stdout or os.Stderr fails.
func TestAddWritersNonWrappedStdoutStderr(t *testing.T) {
	sh := gosh.NewShell(gosh.Opts{Fatalf: makeFatalf(t), Logf: t.Logf})
	defer sh.Cleanup()

	c := sh.FuncCmd(writeMoreFunc)
	setsErr(t, sh, func() { c.AddStdoutWriter(os.Stdout) })
	setsErr(t, sh, func() { c.AddStdoutWriter(os.Stderr) })
	setsErr(t, sh, func() { c.AddStderrWriter(os.Stdout) })
	setsErr(t, sh, func() { c.AddStderrWriter(os.Stderr) })
}

func TestCombinedOutput(t *testing.T) {
	sh := gosh.NewShell(gosh.Opts{Fatalf: makeFatalf(t), Logf: t.Logf})
	defer sh.Cleanup()

	c := sh.FuncCmd(writeFunc, true, true)
	buf := &bytes.Buffer{}
	c.AddStdoutWriter(gosh.NopWriteCloser(buf))
	c.AddStderrWriter(gosh.NopWriteCloser(buf))
	output := c.CombinedOutput()
	// Note, we can't assume any particular ordering of stdout and stderr, so we
	// simply check the length of the combined output.
	eq(t, len(output), 4)
	// The ordering must be the same, regardless of how we captured the combined
	// output.
	eq(t, output, buf.String())
}

func TestOutputDir(t *testing.T) {
	sh := gosh.NewShell(gosh.Opts{Fatalf: makeFatalf(t), Logf: t.Logf})
	defer sh.Cleanup()

	dir := sh.MakeTempDir()
	c := sh.FuncCmd(writeFunc, true, true)
	c.OutputDir = dir
	c.Run()

	matches, err := filepath.Glob(filepath.Join(dir, "*.stdout"))
	ok(t, err)
	eq(t, len(matches), 1)
	stdout, err := ioutil.ReadFile(matches[0])
	ok(t, err)
	eq(t, string(stdout), "AA")

	matches, err = filepath.Glob(filepath.Join(dir, "*.stderr"))
	ok(t, err)
	eq(t, len(matches), 1)
	stderr, err := ioutil.ReadFile(matches[0])
	ok(t, err)
	eq(t, string(stderr), "BB")
}

type countingWriteCloser struct {
	io.Writer
	count int
}

func (wc *countingWriteCloser) Close() error {
	wc.count++
	return nil
}

// Tests that Close is called exactly once on a given WriteCloser, even if that
// WriteCloser is passed to Add{Stdout,Stderr}Writer multiple times.
func TestAddWritersCloseOnce(t *testing.T) {
	sh := gosh.NewShell(gosh.Opts{Fatalf: makeFatalf(t), Logf: t.Logf})
	defer sh.Cleanup()

	c := sh.FuncCmd(writeFunc, true, true)
	buf := &bytes.Buffer{}
	wc := &countingWriteCloser{Writer: buf}
	c.AddStdoutWriter(wc)
	c.AddStdoutWriter(wc)
	c.AddStderrWriter(wc)
	c.AddStderrWriter(wc)
	c.Run()
	// Note, we can't assume any particular ordering of stdout and stderr, so we
	// simply check the length of the combined output.
	eq(t, len(buf.String()), 8)
	eq(t, wc.count, 1)
}

// Tests piping from one Cmd's stdout/stderr to another's stdin. It should be
// possible to wait on just the last Cmd.
func TestPiping(t *testing.T) {
	sh := gosh.NewShell(gosh.Opts{Fatalf: makeFatalf(t), Logf: t.Logf})
	defer sh.Cleanup()

	echo := sh.FuncCmd(echoFunc)
	echo.Args = append(echo.Args, "foo")
	cat := sh.FuncCmd(catFunc)
	echo.AddStdoutWriter(cat.StdinPipe())
	echo.Start()
	eq(t, cat.Stdout(), "foo\n")

	// This time, pipe both stdout and stderr to cat's stdin.
	c := sh.FuncCmd(writeFunc, true, true)
	cat = sh.FuncCmd(catFunc)
	c.AddStdoutWriter(cat.StdinPipe())
	c.AddStderrWriter(cat.StdinPipe())
	c.Start()
	// Note, we can't assume any particular ordering of stdout and stderr, so we
	// simply check the length of the combined output.
	eq(t, len(cat.Stdout()), 4)
}

func TestSignal(t *testing.T) {
	sh := gosh.NewShell(gosh.Opts{Fatalf: makeFatalf(t), Logf: t.Logf})
	defer sh.Cleanup()

	for _, d := range []time.Duration{0, time.Second} {
		for _, s := range []os.Signal{os.Interrupt, os.Kill} {
			fmt.Println(d, s)
			c := sh.FuncCmd(sleepFunc, d, 0)
			c.Start()
			// Wait for a bit to allow the zero-sleep commands to exit.
			time.Sleep(100 * time.Millisecond)
			c.Signal(s)
			// Wait should succeed as long as the exit code was 0, regardless of
			// whether the signal arrived or the process had already exited.
			if s == os.Interrupt {
				// Note: We don't call Wait in the {d: 0, s: os.Kill} case because doing
				// so makes the test flaky on slow systems.
				c.Wait()
			} else if d == time.Second {
				setsErr(t, sh, func() { c.Wait() })
			}
		}
	}

	// Signal should fail if Wait has been called.
	c := sh.FuncCmd(sleepFunc, time.Duration(0), 0)
	c.Run()
	setsErr(t, sh, func() { c.Signal(os.Interrupt) })
}

func TestTerminate(t *testing.T) {
	sh := gosh.NewShell(gosh.Opts{Fatalf: makeFatalf(t), Logf: t.Logf})
	defer sh.Cleanup()

	for _, d := range []time.Duration{0, time.Second} {
		for _, s := range []os.Signal{os.Interrupt, os.Kill} {
			fmt.Println(d, s)
			c := sh.FuncCmd(sleepFunc, d, 0)
			c.Start()
			// Wait for a bit to allow the zero-sleep commands to exit.
			time.Sleep(100 * time.Millisecond)
			// Terminate should succeed regardless of the exit code, and regardless of
			// whether the signal arrived or the process had already exited.
			c.Terminate(s)
		}
	}

	// Terminate should fail if Wait has been called.
	c := sh.FuncCmd(sleepFunc, time.Duration(0), 0)
	c.Run()
	setsErr(t, sh, func() { c.Terminate(os.Interrupt) })
}

func TestShellWait(t *testing.T) {
	sh := gosh.NewShell(gosh.Opts{Fatalf: makeFatalf(t), Logf: t.Logf})
	defer sh.Cleanup()

	d0 := time.Duration(0)
	d200 := 200 * time.Millisecond

	c0 := sh.FuncCmd(sleepFunc, d0, 0)   // not started
	c1 := sh.FuncCmd(sleepFunc, d0, 0)   // failed to start
	c2 := sh.FuncCmd(sleepFunc, d200, 0) // running and will succeed
	c3 := sh.FuncCmd(sleepFunc, d200, 1) // running and will fail
	c4 := sh.FuncCmd(sleepFunc, d0, 0)   // succeeded
	c5 := sh.FuncCmd(sleepFunc, d0, 0)   // succeeded, called wait
	c6 := sh.FuncCmd(sleepFunc, d0, 1)   // failed
	c7 := sh.FuncCmd(sleepFunc, d0, 1)   // failed, called wait

	c3.ExitErrorIsOk = true
	c6.ExitErrorIsOk = true
	c7.ExitErrorIsOk = true

	// Configure the "failed to start" command.
	c1.StdinPipe()
	c1.Stdin = "foo"
	setsErr(t, sh, func() { c1.Start() })

	// Start commands, then wait for them to exit.
	for _, c := range []*gosh.Cmd{c2, c3, c4, c5, c6, c7} {
		c.Start()
	}
	// Wait for a bit to allow the zero-sleep commands to exit.
	time.Sleep(100 * time.Millisecond)
	c5.Wait()
	c7.Wait()
	sh.Wait()

	// It should be possible to run existing unstarted commands, and to create and
	// run new commands, after calling Shell.Wait.
	c0.Run()
	sh.FuncCmd(sleepFunc, d0, 0).Run()
	sh.FuncCmd(sleepFunc, d0, 0).Start()

	// Call Shell.Wait again.
	sh.Wait()
}

func TestExitErrorIsOk(t *testing.T) {
	sh := gosh.NewShell(gosh.Opts{Fatalf: makeFatalf(t), Logf: t.Logf})
	defer sh.Cleanup()

	// Exit code 0 is not an error.
	c := sh.FuncCmd(exitFunc, 0)
	c.Run()
	ok(t, c.Err)
	ok(t, sh.Err)

	// Exit code 1 is an error.
	c = sh.FuncCmd(exitFunc, 1)
	c.ExitErrorIsOk = true
	c.Run()
	nok(t, c.Err)
	ok(t, sh.Err)

	// If ExitErrorIsOk is false, exit code 1 triggers sh.HandleError.
	c = sh.FuncCmd(exitFunc, 1)
	setsErr(t, sh, func() { c.Run() })
	nok(t, c.Err)
}

// Tests that sh.Ok panics under various conditions.
func TestOkPanics(t *testing.T) {
	func() { // errDidNotCallNewShell
		sh := gosh.Shell{}
		defer func() { neq(t, recover(), nil) }()
		sh.Ok()
	}()
	func() { // errShellErrIsNotNil
		sh := gosh.NewShell(gosh.Opts{Fatalf: t.Logf})
		defer sh.Cleanup()
		sh.Err = fakeError
		defer func() { neq(t, recover(), nil) }()
		sh.Ok()
	}()
	func() { // errAlreadyCalledCleanup
		sh := gosh.NewShell(gosh.Opts{Fatalf: t.Logf})
		sh.Cleanup()
		defer func() { neq(t, recover(), nil) }()
		sh.Ok()
	}()
}

// Tests that sh.HandleError panics under various conditions.
func TestHandleErrorPanics(t *testing.T) {
	func() { // errDidNotCallNewShell
		sh := gosh.Shell{}
		defer func() { neq(t, recover(), nil) }()
		sh.HandleError(fakeError)
	}()
	func() { // errShellErrIsNotNil
		sh := gosh.NewShell(gosh.Opts{Fatalf: t.Logf})
		defer sh.Cleanup()
		sh.Err = fakeError
		defer func() { neq(t, recover(), nil) }()
		sh.HandleError(fakeError)
	}()
	func() { // errAlreadyCalledCleanup
		sh := gosh.NewShell(gosh.Opts{Fatalf: t.Logf})
		sh.Cleanup()
		defer func() { neq(t, recover(), nil) }()
		sh.HandleError(fakeError)
	}()
}

// Tests that sh.Cleanup panics under various conditions.
func TestCleanupPanics(t *testing.T) {
	func() { // errDidNotCallNewShell
		sh := gosh.Shell{}
		defer func() { neq(t, recover(), nil) }()
		sh.Cleanup()
	}()
}

// Tests that sh.Cleanup succeeds even if sh.Err is not nil.
func TestCleanupAfterError(t *testing.T) {
	sh := gosh.NewShell(gosh.Opts{Fatalf: makeFatalf(t), Logf: t.Logf})
	sh.Err = fakeError
	sh.Cleanup()
}

// Tests that sh.Cleanup can be called multiple times.
func TestMultipleCleanup(t *testing.T) {
	sh := gosh.NewShell(gosh.Opts{Fatalf: makeFatalf(t), Logf: t.Logf})
	sh.Cleanup()
	sh.Cleanup()
}

func TestMain(m *testing.M) {
	gosh.InitMain()
	os.Exit(m.Run())
}
