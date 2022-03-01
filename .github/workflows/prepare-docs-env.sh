#!/usr/bin/env sh

set -eu

pythonVersion="$(./vars.mk FROM=docs python_version)"

cat <<EOF >>"$GITHUB_ENV"
PYTHON_VERSION=$pythonVersion
EOF

# shellcheck disable=SC1090
. "$GITHUB_ENV"

echo ::group::OS Environment
env | sort
echo ::endgroup::

echo ::group::Configure git
git config --local user.email "action@github.com"
git config --local user.name "GitHub Action"
echo ::endgroup::
