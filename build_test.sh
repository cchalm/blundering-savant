#!/bin/bash
go build ./cmd/blundering-savant 2>&1 || echo "Build failed"
cd cmd/blundering-savant
go test -run TestGetFileTree 2>&1