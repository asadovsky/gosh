# Gosh

Oh my gosh, no more shell scripts!

GoDoc: https://godoc.org/github.com/asadovsky/gosh

Gosh is a Go library for running and managing processes: start them, wait for
them to exit, capture and examine their output streams, pipe messages between
them, terminate them (e.g. on SIGINT), and so forth.

Gosh is meant to be used in situations where you might otherwise be tempted to
write a shell script. It is not a framework. It will not solve all your
problems.

## Development

    GOPATH=~/dev/go go test github.com/asadovsky/gosh/...
    GOPATH=~/dev/go go vet github.com/asadovsky/gosh/...

    GOPATH=~/dev/go go install github.com/asadovsky/gosh/...
    GOPATH=~/dev/go ~/dev/go/bin/example
