// Copyright 2015 The Vanadium Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"

	"github.com/asadovsky/gosh"
	"github.com/asadovsky/gosh/internal/gosh_example_lib"
)

var addr = flag.String("addr", "localhost:8080", "server addr")

func main() {
	gosh.MaybeWatchParent()
	flag.Parse()
	lib.Get(*addr)
}
