package gosh_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/asadovsky/gosh"
)

func eq(t *testing.T, got, want interface{}) {
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func env(sh gosh.Shell) string {
	return strings.Join(sh.Env(), " ")
}

func TestEnv(t *testing.T) {
	sh, cleanup := gosh.New()
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

func TestHello(t *testing.T) {
}
