#!/usr/bin/env bash
set -eux
K0S_BIN="${GITHUB_WORKSPACE}/k0s"
PRIVATE_KEY="${GITHUB_WORKSPACE}/inttest/terraform/test-cluster/aws_private.pem"

with_ssh_opts() {
  local cmd="$1"
  shift 1

  "$cmd" -o StrictHostKeyChecking=no -i "$PRIVATE_KEY" "$@"
}

touch -- "$K0S_BIN"
# prepare private key
chmod 0600 -- "$PRIVATE_KEY"

# terraform's github actions print debug information on the first line
# this command removes it
sed -i '1d' out.json

controller_ips=$(jq -r '.["controller_external_ip"].value[]' <out.json 2>/dev/null)
worker_ips=$(jq -r '.["worker_external_ip"].value[]' <out.json 2>/dev/null)

# remove single quotes, if exists
# not sure about those shellcheck warnings here
# shellcheck disable=SC2206
controller_ips=(${controller_ips[@]//\'/})
# not sure about those shellcheck warnings here
# shellcheck disable=SC2206
worker_ips=(${worker_ips[@]//\'/})

# Save To File
# Sounds bad: Expanding an array without an index only gives the first element
# shellcheck disable=SC2128
printf '%s\n' "$controller_ips" >CTRL_IPS
# Sounds bad: Expanding an array without an index only gives the first element
# shellcheck disable=SC2128
printf '%s\n' "$worker_ips" >WORKER_IPS

for controller in "${controller_ips[@]}"
do
  with_ssh_opts scp "$K0S_BIN" ubuntu@"$controller":
  with_ssh_opts ssh ubuntu@"$controller" "sudo scp k0s /usr/local/bin/"
done

for worker in "${worker_ips[@]}"
do
  with_ssh_opts scp "$K0S_BIN" ubuntu@"$worker":
  with_ssh_opts ssh ubuntu@"$worker" "sudo scp k0s /usr/local/bin/"
done
