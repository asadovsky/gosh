# Gosh

Gosh is a Go package for running and managing processes.

GoDoc: https://godoc.org/github.com/asadovsky/gosh

## Updating

    GOPATH=~/dev/go
    cd $GOPATH/src/github.com/asadovsky/gosh
    rm -rf *
    cp -rf $JIRI_ROOT/release/go/src/v.io/x/lib/gosh/ ./
    cp $JIRI_ROOT/release/go/src/v.io/x/lib/LICENSE ./
    find-replace "v.io/x/lib/gosh" "github.com/asadovsky/gosh"
    git checkout -- README.md
    go test github.com/asadovsky/gosh/...
