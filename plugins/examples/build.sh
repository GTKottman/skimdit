#!/bin/sh
# Builds the example plugins into plugins/examples/*.wasm.
set -e
cd "$(dirname "$0")"
for d in */; do
	name=${d%/}
	GOOS=wasip1 GOARCH=wasm go build -trimpath -ldflags="-s -w" -o "$name.wasm" "./$name"
	echo "built plugins/examples/$name.wasm"
done
