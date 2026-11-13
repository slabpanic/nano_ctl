#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd)"

cd "$PROJECT_ROOT"
# systemd grants CAP_SYS_RAWIO to this launcher for the SDK. Remove it from the
# build subprocess tree; only the final nano_ctl process keeps the capability.
setpriv --inh-caps=-sys_rawio --ambient-caps=-sys_rawio -- \
	"$PROJECT_ROOT/scripts/build.sh" "$PROJECT_ROOT/dist"
exec "$PROJECT_ROOT/dist/nano_ctl"
