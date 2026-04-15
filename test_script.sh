#!/bin/bash
# Ce script compile les binaires pour Linux
export GOOS=linux
export GOARCH=amd64

echo "Compiling controller..."
go build -o build/controller cmd/controller/main.go
echo "Compiling dataplane..."
go build -o build/dataplane cmd/dataplane/main.go
echo "Compiling worker..."
go build -o build/worker cmd/worker/main.go

echo "Build successful. You can find the binaries in the build/ directory."
