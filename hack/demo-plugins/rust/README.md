# Demo k0s plugin using Rust

A demonstration on how to create a WASI preview 2 k0s plugin using Rust.

## Features

* `hello-stdout`  
  Prints stuff to stdout using both the `wasi:cli/stdout` WIT types as well as
  the Rust standard library.

* `runtime-env`  
  Prints program arguments and process environment variables, and traverses the
  root file system using Rust's stdlib.

* `github-api-get`  
  Queries `https://api.github.com` using the `wasi:http/outgoing-handler` WIT
  types, parses the JSON response and pretty-prints it.  

## Using wasi_snapshot_preview1

This is using Rust's WASI Preview 1 target (`wasm32-wasi` as of Rust 1.77),
which is then adapted to WASI Preview 2 as described in the [`wasi`
crate](https://docs.rs/wasi/0.13.0+wasi-0.2.0/wasi/index.html#using-this-crate).

```console
$ cargo build --target wasm32-wasi --all-features
   Compiling [...]
   Compiling k0s-demo-plugin v0.0.1-dev (.)
    Finished release [optimized] target(s) in xx.xxs

$ wget -q https://github.com/bytecodealliance/wasmtime/releases/download/v19.0.0/wasi_snapshot_preview1.command.wasm

$ wasm-tools component new target/wasm32-wasi/debug/k0s-demo-plugin.wasm --adapt wasi_snapshot_preview1.command.wasm -o plugin.wasm

$ wasm-tools component wit plugin.wasm
package root:component;

world root {
  import wasi:io/poll@0.2.0;
  import wasi:io/error@0.2.0;
  import wasi:io/streams@0.2.0;
  import wasi:http/types@0.2.0;
  import wasi:http/outgoing-handler@0.2.0;
  import wasi:cli/environment@0.2.0;
  import wasi:cli/exit@0.2.0;
  import wasi:cli/stdin@0.2.0;
  import wasi:cli/stdout@0.2.0;
  import wasi:cli/stderr@0.2.0;
  import wasi:clocks/wall-clock@0.2.0;
  import wasi:filesystem/types@0.2.0;
  import wasi:filesystem/preopens@0.2.0;

  export wasi:cli/run@0.2.0;
}

$ wasmtime run -S http=y plugin.wasm
Hello, wasi:cli/stdout!
Hello, Rust stdlib stdout!

Arguments:
"plugin.wasm"

Environment Variables:

File System:
!       Error reading dir: failed to find a pre-opened file descriptor through which "/" could be opened

{
  "authorizations_url": "https://api.github.com/authorizations",
  "code_search_url": "https://api.github.com/search/code?q={query}{&page,per_page,sort,order}",
  "commit_search_url": "https://api.github.com/search/commits?q={query}{&page,per_page,sort,order}",
  "current_user_authorizations_html_url": "https://github.com/settings/connections/applications{/client_id}",
  "current_user_repositories_url": "https://api.github.com/user/repos{?type,page,per_page,sort}",
  "current_user_url": "https://api.github.com/user",
  "emails_url": "https://api.github.com/user/emails",
  "emojis_url": "https://api.github.com/emojis",
  "events_url": "https://api.github.com/events",
  "feeds_url": "https://api.github.com/feeds",
  "followers_url": "https://api.github.com/user/followers",
  "following_url": "https://api.github.com/user/following{/target}",
  "gists_url": "https://api.github.com/gists{/gist_id}",
  "hub_url": "https://api.github.com/hub",
  "issue_search_url": "https://api.github.com/search/issues?q={query}{&page,per_page,sort,order}",
  "issues_url": "https://api.github.com/issues",
  "keys_url": "https://api.github.com/user/keys",
  "label_search_url": "https://api.github.com/search/labels?q={query}&repository_id={repository_id}{&page,per_page}",
  "notifications_url": "https://api.github.com/notifications",
  "organization_repositories_url": "https://api.github.com/orgs/{org}/repos{?type,page,per_page,sort}",
  "organization_teams_url": "https://api.github.com/orgs/{org}/teams",
  "organization_url": "https://api.github.com/orgs/{org}",
  "public_gists_url": "https://api.github.com/gists/public",
  "rate_limit_url": "https://api.github.com/rate_limit",
  "repository_search_url": "https://api.github.com/search/repositories?q={query}{&page,per_page,sort,order}",
  "repository_url": "https://api.github.com/repos/{owner}/{repo}",
  "starred_gists_url": "https://api.github.com/gists/starred",
  "starred_url": "https://api.github.com/user/starred{/owner}{/repo}",
  "topic_search_url": "https://api.github.com/search/topics?q={query}{&page,per_page}",
  "user_organizations_url": "https://api.github.com/user/orgs",
  "user_repositories_url": "https://api.github.com/users/{user}/repos{?type,page,per_page,sort}",
  "user_search_url": "https://api.github.com/search/users?q={query}{&page,per_page,sort,order}",
  "user_url": "https://api.github.com/users/{user}"
}

YOLO!

```
