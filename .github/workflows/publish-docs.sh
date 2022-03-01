#!/usr/bin/env sh

set -eu

install_dependencies() {
  python -m pip install --upgrade pip
  pip install -r docs/requirements_release.txt
  pip install git+https://"$GH_TOKEN"@github.com/lensapp/mkdocs-material-insiders.git
  go install github.com/k0sproject/version/cmd/k0s_sort@v0.2.2
}

generate() {
  make -C docs docs clean-k0s
}

deploy_main() {
  mike deploy --push main
}

deploy_release() {
  TARGET_VERSION="${GITHUB_REF##refs/tags/}"
  echo mike deploy --push --update-aliases "$TARGET_VERSION" latest
  echo mike set-default --push "$TARGET_VERSION"
}

deploy_version() {
  echo mike deploy --push --force "$TARGET_VERSION"
}

main() {
  if [ -n "${TARGET_VERSION-}" ]; then
    deploy=deploy_version
  else
    case "$GITHUB_REF" in
    refs/heads/main)
      deploy=deploy_main
      TARGET_VERSION=latest
      ;;
    refs/tags/v*)
      deploy=deploy_release
      TARGET_VERSION=latest
      ;;

    *)
      echo "Error: don't know how to deploy $GITHUB_REF" >&2
      exit 1
      ;;
    esac
  fi

  echo ::group::Install dependencies
  install_dependencies
  echo ::endgroup::

  echo ::group::Generate
  generate
  echo ::endgroup::

  echo ::group::Deploy
  $deploy
  echo ::endgroup::
}

main
