package main

import (
	"flag"

	"github.com/asadovsky/gosh"
	"github.com/asadovsky/gosh/example/lib"
)

var addr = flag.String("addr", "localhost:8080", "server addr")

func main() {
	gosh.WatchParent()
	flag.Parse()
	lib.Get(*addr)
}
