# Terraform modules for k0s OS testing

## Requirements

* terraform >= 1.4

For the local k0sctl plumbing:

* A POSIXish environment (env, sh, echo, printf)
* k0sctl >= 0.15
* jq >= 1.6

## GitHub Actions workflow

There's a GitHub Actions workflow available in [ostests.yaml]. In order to be
used, the repository needs to have valid AWS credentials available in its
secrets:

* AWS_ACCESS_KEY_ID
* AWS_SECRET_ACCESS_KEY
* AWS_SESSION_TOKEN

[ostests.yaml]: ../../.github/workflows/ostests.yaml

### Launch a workflow run

Custom workflow runs can be launched using [gh]:

```console
$ gh workflow run ostests.yaml --ref some/experimental/branch -f oses='["alpine_317"]'
✓ Created workflow_dispatch event for ostests.yaml at some/experimental/branch

To see runs for this workflow, try: gh run list --workflow=ostests.yaml
```

[gh]: https://github.com/cli/cli
