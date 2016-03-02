# Gosh

Gosh is a Go package for running and managing processes.

GoDoc: https://godoc.org/github.com/asadovsky/gosh

## Updating

    SRCDIR=`mktemp -d`
    GOPATH=$SRCDIR go get -d v.io/x/lib/gosh
    HEAD=`cd $SRCDIR/src/v.io/x/lib && git rev-parse HEAD`

    GOPATH=~/dev/go
    cd $GOPATH/src/github.com/asadovsky/gosh
    rm -rf *
    rsync -r $SRCDIR/src/v.io/x/lib/gosh/ ./
    cp $SRCDIR/src/v.io/x/lib/LICENSE ./
    find-replace "v.io/x/lib/gosh" "github.com/asadovsky/gosh"
    git checkout -- README.md
    mkdir -p vendor/v.io/x/lib/lookpath
    rsync -r $SRCDIR/src/v.io/x/lib/lookpath/ ./vendor/v.io/x/lib/lookpath/

    GO15VENDOREXPERIMENT=1
    go test github.com/asadovsky/gosh/...
    go vet github.com/asadovsky/gosh/...
    git add -A && git commit -m "pull $HEAD" && git push
