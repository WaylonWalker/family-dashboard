#!/usr/bin/env bash
# Explicit deploy command; Argo's Git desired state remains the source of truth.
set -euo pipefail
image="${1:?expected fully qualified image:tag}"

for binary in git just kubectl argocd "${CONTAINER_ENGINE:-podman}" python3; do
  command -v "$binary" >/dev/null || { echo "Missing tool: $binary" >&2; exit 2; }
done

if [[ "$(git branch --show-current)" != "main" ]]; then
  echo "Refusing deploy: switch to the main branch first." >&2
  exit 2
fi
if [[ -n "$(git status --porcelain)" ]]; then
  echo "Refusing deploy: commit or stash local changes first." >&2
  exit 2
fi

expected_tag="sha-$(git rev-parse HEAD)"
if [[ "${image##*:}" != "$expected_tag" ]]; then
  echo "Refusing deploy: image must be tagged with $expected_tag for this checkout." >&2
  exit 2
fi

git fetch origin main
if [[ "$(git rev-parse HEAD)" != "$(git rev-parse origin/main)" ]]; then
  echo "Refusing deploy: main is not exactly up-to-date with origin/main." >&2
  exit 2
fi

kubectl -n argocd get application family-dashboard >/dev/null || {
  echo "Argo Application missing. Install the bootstrap YAML from docs/homelab-argo first." >&2
  exit 2
}

just check
just container-build
just container-push
if [[ "${CONTAINER_ENGINE:-podman}" == podman ]]; then
  podman pull --tls-verify=false "$image" >/dev/null
  digest=$(podman image inspect --format '{{.Digest}}' "$image")
else
  "${CONTAINER_ENGINE:-docker}" pull "$image" >/dev/null
  repo_digest=$("${CONTAINER_ENGINE:-docker}" image inspect --format '{{index .RepoDigests 0}}' "$image")
  digest="${repo_digest##*@}"
fi
if [[ ! "$digest" =~ ^sha256:[a-f0-9]{64}$ ]]; then
  echo "Could not resolve a registry manifest digest for $image" >&2
  exit 2
fi
just pin-image "$image@$digest"
if ! git diff --quiet -- k8s/deployment.yaml; then
  git add k8s/deployment.yaml
  git commit -m "chore(deploy): promote family-dashboard to ${image##*:}"
  git push origin main
else
  echo "Deployment image already pinned; no Git commit required."
fi

# This application starts manual-sync. Explicitly sync after the pin is in Git.
argocd app get family-dashboard --hard-refresh >/dev/null
argocd app sync family-dashboard
kubectl -n "${NAMESPACE:-family-dashboard}" rollout status deployment/family-dashboard --timeout=240s
printf 'Deployed %s via Argo CD.\n' "$image"
