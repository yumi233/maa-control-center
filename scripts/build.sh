#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
(cd web && npm ci && npm run build)
mkdir -p internal/ui/dist bin
cp -R web/dist/. internal/ui/dist/
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o bin/maa-server-linux-amd64 ./cmd/server
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags='-s -w -H=windowsgui' -o bin/maa-agent-windows-amd64.exe ./cmd/agent
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o bin/maa-setup-linux-amd64 ./cmd/setup
go build -trimpath -ldflags='-s -w' -o bin/maa-server ./cmd/server
go build -trimpath -ldflags='-s -w' -o bin/maa-setup ./cmd/setup
echo 'Built NAS server, Windows agent, setup utility and local preview binaries.'
