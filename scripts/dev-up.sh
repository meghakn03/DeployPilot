#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLUSTER_NAME="${DEPLOYPILOT_KIND_CLUSTER:-deploypilot}"
IMAGE="${DEPLOYPILOT_IMAGE:-deploypilot:dev}"
API_TOKEN="${DEPLOYPILOT_API_TOKEN:-local-dev-token}"
LOCAL_PORT="${DEPLOYPILOT_LOCAL_PORT:-8082}"

for command in docker kind kubectl curl; do
  if ! command -v "$command" >/dev/null 2>&1; then
    echo "Required command not found: $command" >&2
    exit 1
  fi
done

if command -v lsof >/dev/null 2>&1 && lsof -nP -iTCP:"$LOCAL_PORT" -sTCP:LISTEN -t >/dev/null 2>&1; then
  requested_port="$LOCAL_PORT"
  while lsof -nP -iTCP:"$LOCAL_PORT" -sTCP:LISTEN -t >/dev/null 2>&1; do
    LOCAL_PORT=$((LOCAL_PORT + 1))
  done
  echo "Port $requested_port is busy; using localhost:$LOCAL_PORT instead."
fi

if ! docker info >/dev/null 2>&1; then
  echo "Docker is installed but its daemon is unavailable. Start Docker Desktop and retry 'make up'." >&2
  exit 1
fi

if ! kind get clusters | grep -Fxq "$CLUSTER_NAME"; then
  kind create cluster --name "$CLUSTER_NAME"
fi

cd "$ROOT"
docker build -t "$IMAGE" .
kind load docker-image "$IMAGE" --name "$CLUSTER_NAME"

kubectl apply -f config/crd.yaml
kubectl wait --for condition=Established --timeout=60s crd/deploymentrequests.deploypilot.io
kubectl apply -k config
kubectl create secret generic deploypilot-api -n deploypilot-system \
  --from-literal="token=$API_TOKEN" --dry-run=client -o yaml | kubectl apply -f -

# Restart the API so it reads the token secret created above.
kubectl rollout restart deployment/deploypilot-api -n deploypilot-system
kubectl rollout status deployment/deploypilot-controller -n deploypilot-system --timeout=180s
kubectl rollout status deployment/deploypilot-api -n deploypilot-system --timeout=180s

echo "DeployPilot is running. API token: $API_TOKEN"
URL="http://localhost:$LOCAL_PORT"
FORWARD_LOG="${TMPDIR:-/tmp}/deploypilot-port-forward-$LOCAL_PORT.log"
kubectl port-forward -n deploypilot-system service/deploypilot-api "$LOCAL_PORT:8082" >"$FORWARD_LOG" 2>&1 &
FORWARD_PID=$!
cleanup() {
  kill "$FORWARD_PID" 2>/dev/null || true
  wait "$FORWARD_PID" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

ready=false
for _ in $(seq 1 30); do
  if ! kill -0 "$FORWARD_PID" 2>/dev/null; then
    cat "$FORWARD_LOG" >&2
    echo "Port forwarding stopped before the API became reachable." >&2
    exit 1
  fi
  if curl --silent --show-error --fail "$URL/healthz" >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 1
done
if [[ "$ready" != true ]]; then
  cat "$FORWARD_LOG" >&2
  echo "The API did not become reachable at $URL within 30 seconds." >&2
  exit 1
fi

echo "Frontend and API are ready at $URL"
if command -v open >/dev/null 2>&1; then
  open "$URL"
elif command -v xdg-open >/dev/null 2>&1; then
  xdg-open "$URL" >/dev/null 2>&1 || true
fi
echo "Enter the API token in the browser. Press Ctrl-C to stop forwarding."
wait "$FORWARD_PID"
