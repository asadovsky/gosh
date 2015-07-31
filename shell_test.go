package gosh_test

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/asadovsky/gosh"
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

func env(sh gosh.Shell) string {
	return strings.Join(sh.Env(), " ")
}

func TestEnv(t *testing.T) {
	sh, cleanup, _ := gosh.New(gosh.ShellOpts{T: t})
	defer cleanup()
	eq(t, sh.Get("FOO"), "")
	eq(t, env(sh), "")
	sh.Set("FOO=1")
	eq(t, sh.Get("FOO"), "1")
	eq(t, sh.Get("BAR"), "") // not in env
	eq(t, env(sh), "FOO=1")
	sh.Set("BAR=2")
	eq(t, sh.Get("FOO"), "1")
	eq(t, sh.Get("BAR"), "2")
	eq(t, env(sh), "BAR=2 FOO=1")
	sh.Set("FOO=0")
	eq(t, env(sh), "BAR=2 FOO=0")
	sh.Set("FOO=3", "BAR=4", "BAZ=5")
	eq(t, env(sh), "BAR=4 BAZ=5 FOO=3")
	sh.Set("FOO=", "BAR=6", "BAZ=") // unset FOO and BAZ
	eq(t, env(sh), "BAR=6")
}

func TestEnvSort(t *testing.T) {
	sh, cleanup, _ := gosh.New(gosh.ShellOpts{T: t})
	defer cleanup()
	sh.Set("FOO4=4")
	sh.Set("FOO=bar")
	sh.Set("FOOD=D")
	eq(t, env(sh), "FOO=bar FOO4=4 FOOD=D")
}

func TestPushdPopd(t *testing.T) {
	sh, cleanup, err := gosh.New(gosh.ShellOpts{})
	ok(t, err)
	defer cleanup()
	startDir, err := os.Getwd()
	parentDir := filepath.Dir(startDir)
	ok(t, err)
	neq(t, startDir, parentDir)
	ok(t, sh.Pushd(parentDir))
	cwd, err := os.Getwd()
	ok(t, err)
	eq(t, cwd, parentDir)
	ok(t, sh.Pushd(startDir))
	cwd, err = os.Getwd()
	ok(t, err)
	eq(t, cwd, startDir)
	ok(t, sh.Popd())
	cwd, err = os.Getwd()
	ok(t, err)
	eq(t, cwd, parentDir)
	ok(t, sh.Popd())
	cwd, err = os.Getwd()
	ok(t, err)
	eq(t, cwd, startDir)
	nok(t, sh.Popd())
}
