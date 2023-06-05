# Terraform modules for k0s OS testing

## Requirements

* Terraform >= 1.4

For the local k0sctl plumbing:

* A POSIXish environment (env, sh, echo, printf)
* k0sctl (tested with ~= 0.15)
* jq (tested with ~= 1.6)

For the AWS backend:

* Have the CLI credentials setup, in the usual AWS CLI way.
* Have a configured default region. That region will be targeted by Terraform.

## GitHub Actions workflow

There's a GitHub Actions workflow available in [ostests.yaml]. It will execute a
test matrix, deploy OS stacks, provision k0s clusters and perform conformance
tests against those. In order to be used, the repository needs to have valid AWS
credentials available in its secrets:

* `AWS_ACCESS_KEY_ID`
* `AWS_SECRET_ACCESS_KEY`
* `AWS_SESSION_TOKEN`

[ostests.yaml]: ../../.github/workflows/ostests.yaml

### Launch a workflow run

Custom workflow runs can be launched using [gh]:

```console
$ gh workflow run ostests.yaml --ref some/experimental/branch -f oses='["alpine_317"]'
✓ Created workflow_dispatch event for ostests.yaml at some/experimental/branch

To see runs for this workflow, try: gh run list --workflow=ostests.yaml
```

[gh]: https://github.com/cli/cli

## Supported operating systems

* `alpine_317`: Alpine Linux 3.17
* `centos_7`: CentOS Linux 7 (Core)
* `centos_8`: CentOS Stream 8
* `centos_9`: CentOS Stream 9
* `debian_10`: Debian GNU/Linux 10 (buster)
* `debian_11`: Debian GNU/Linux 11 (bullseye)
* `fcos_38`: Fedora CoreOS 38
* `fedora_38`: Fedora Linux 38 (Cloud Edition)
* `flatcar`: Flatcar Container Linux by Kinvolk
* `rhel_7`: Red Hat Enterprise Linux Server 7.9 (Maipo)
* `rhel_8`: Red Hat Enterprise Linux 8.6 (Ootpa)
* `rhel_9`: Red Hat Enterprise Linux 9.0 (Plow)
* `rocky_8`: Rocky Linux 8.7 (Green Obsidian)
* `rocky_9`: Rocky Linux 9.2 (Blue Onyx)
* `ubuntu_2004`: Ubuntu 20.04 LTS
* `ubuntu_2204`: Ubuntu 22.04 LTS
* `ubuntu_2304`: Ubuntu 23.04

### Adding a new operating system

* Navigate to [backends/aws/modules/os/](backends/aws/modules/os/) and add a new
  file `os_<the-os-id>.tf`. Have a look at the other `os_*.tf` files for how it
  should look like.
* Add a new OS entry to [backends/aws/modules/os/main.tf]
  (backends/aws/modules/os/main.tf).
* Update this README.
* Test it: Be sure to have the requisites ready, as described at the top of this
  README, then do `TF_VAR_os=<the-os-id> terraform apply`. When done, don't
  forget to clean up: `TF_VAR_os=<the-os-id> terraform destroy`.
* Update the GitHub workflow `ostests.yaml` with the new OS ID.
