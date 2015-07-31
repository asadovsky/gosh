package gosh_test

import (
	"reflect"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/asadovsky/gosh"
)

func Fatal(t *testing.T, args ...interface{}) {
	debug.PrintStack()
	t.Fatal(args...)
}

func Fatalf(t *testing.T, format string, args ...interface{}) {
	debug.PrintStack()
	t.Fatalf(format, args...)
}

func ok(t *testing.T, err error) {
	if err != nil {
		Fatal(t, err)
	}
}

func eq(t *testing.T, got, want interface{}) {
	if !reflect.DeepEqual(got, want) {
		Fatalf(t, "got %v, want %v", got, want)
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
	sh.Set("FOO=", "BAR=6", "BAZ=")
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

func TestHello(t *testing.T) {
}
