# Agent workflow

## Build and deploy

When the user asks to deploy Family HQ, use the local release command:

```sh
just deploy
```

This runs `just check` locally, builds and pushes an immutable image, updates
the pinned image digest in `k8s/deployment.yaml`, pushes that GitOps change,
syncs Argo CD, and waits for the Kubernetes rollout. GitHub Actions runs
separately; its status is not a prerequisite for this deploy path. Report the
local check and rollout results, and do not imply CI has passed unless you
checked it.

The command requires a clean, current `main` checkout and working registry,
Git, Argo CD, and Kubernetes access. For local development, use `just serve`,
`just check`, and `just build`; see the `justfile` for the canonical commands.

## Land session changes

When the user asks to land completed work, finish in the same session by
committing the task's changes and pushing the current branch to its upstream.
Use the repository's normal branch or pull request flow when one applies. CI
runs independently; its status does not block landing unless the user asks for
a CI-gated release. Report the commit and push result, and state CI status
separately when it is relevant.
