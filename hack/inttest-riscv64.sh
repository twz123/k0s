#!/usr/bin/env sh

set -eu

rootPath=$(pwd -P -- "$(dirname -- "$0")/..")

skipReason() {
  case "$1" in
  check-addons) echo 'needs helm in RISC-V footloose Docker image' ;;
  *airgap*) echo 'need riscv64 support for the airgap image bundle build process' ;;
  *-ap-*) echo 'skip Autopilot for now' ;;
  check-byocri) echo 'needs docker-shim in RISC-V footloose Docker image' ;;
  check-cli) echo 'FIXME needs investigation';;
  *calico*) echo 'needs RISC-V enabled calico multiarch OCI images' ;;
  check-clusterreboot) echo 'FIXME timeout' ;;
  check-cnichange) echo 'FIXME timeout' ;;
  check-kine) echo 'needs puschgateway?!';;
  check-nllb) echo 'no envoy RISC-V OCI image' ;;
  esac
}

make -C "$rootPath"
for check in $("$rootPath/vars.sh" FROM=inttest smoketests); do
  reason=$(skipReason "$check")
  if [ -n "$reason" ]; then
    echo "Skipping $check: $reason" >&2
    continue
  fi

  stampFile="$rootPath/.riscv64.$check.stamp"

  if [ -n "$(find -- "$stampFile" -newer "$rootPath/k0s")" ]; then
    echo "Cached: $check" >&2
    continue
  fi

  if make -C "$rootPath/inttest" TIMEOUT=30m "$check"; then
    touch -- "$stampFile"
  fi
done
