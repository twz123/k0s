#!/usr/bin/env sh

set -eu

goVersion="$(./vars.mk go_version)"

cat <<EOF >>"$GITHUB_ENV"
GO_VERSION=$goVersion
EOF

# shellcheck disable=SC1090
. "$GITHUB_ENV"

echo ::group::OS Environment
env | sort
echo ::endgroup::
