package main

import (
	"github.com/asadovsky/gosh"
	"github.com/asadovsky/gosh/example/lib"
)

func main() {
	gosh.WatchParent()
	gosh.ExitOnTerminationSignal()
	lib.Serve()
}
