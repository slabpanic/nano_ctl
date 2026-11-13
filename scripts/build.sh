#!/usr/bin/env sh
set -eu

output_dir="${1:-dist}"
mkdir -p "$output_dir"
go run github.com/swaggo/swag/cmd/swag@v1.16.6 init \
	--generalInfo main.go \
	--dir . \
	--parseInternal \
	--output routers/httpapi/docs \
	--outputTypes go,json,yaml
CGO_ENABLED=1 go build -trimpath -ldflags='-s -w' -o "$output_dir/nano_ctl" .
install -m 0644 pkg/godmc/libLTDMC.so "$output_dir/libLTDMC.so"
