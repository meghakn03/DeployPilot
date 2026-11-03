**Tech stack:** Go, Kubernetes, Docker, kind, Kustomize, Prometheus, Grafana, GitHub Actions

# DeployPilot

DeployPilot accepts HTTP deployment requests, stores them as namespaced Kubernetes custom resources, and reconciles each request into one Deployment and one Service. It targets a single cluster and HTTP workloads. It does not build images, route external traffic, schedule across clusters, or roll back releases.

## Architecture and policies

The Go API validates and persists `DeploymentRequest` resources. The controller watches those resources and owns the resulting Deployment and Service, so Kubernetes garbage collection removes workloads when a request is deleted. The controller reports ready and desired replicas and a concise `Progressing` or `Available` condition. Failed image pulls, scheduling failures, quota errors, and probe failures remain visible in Kubernetes Pod and Deployment events; the current status surface reports replicas, but does not yet translate detailed Pod events into a `Failed` condition.

The API uses one configured bearer token (`API_TOKEN`). Treat this as an internal service and put it behind a trusted gateway for client identity and network policy. Configure `ALLOWED_NAMESPACES` and `ALLOWED_REGISTRIES` as comma-separated exact namespace names and registry/image prefixes. Empty allowlists permit all values and are suitable only for local development. Resource settings support CPU and memory only; requests are capped at 8 CPU and 16Gi, limits at 16 CPU and 32Gi, and a limit cannot be lower than its request. Secrets are referenced by name and key; literal environment variables are supported but are not logged. API and controller have separate service accounts and roles. The sample API role is bound only in `demo`; add a namespaced Role/RoleBinding and matching allowlist entry for each additional tenant namespace.

The CRD is namespaced and its owner references control cleanup. `v1alpha1` is the initial storage version. Future incompatible changes require a new served version, conversion strategy, and an explicit storage migration before removing v1alpha1. Progressing workloads have no automatic deadline; alert on requests that remain unavailable for a chosen service-level window (example: 15 minutes).

## Local development

Requirements: Go 1.25+, Docker, kubectl, and kind.

For the full local stack, run one command:

```sh
make up
```

It creates or reuses a kind cluster named `deploypilot`, builds and loads the image, installs the CRD and both services, configures a local API token, waits for both Deployments and the API health check, and opens the frontend at `http://localhost:8082` (or the next available port if 8082 is busy). Enter the API token in the UI when prompted. The default token is `local-dev-token`; set `DEPLOYPILOT_API_TOKEN` before `make up` to choose another. Press Ctrl-C to stop port forwarding. The cluster remains available; run `make down` to delete it.

The browser UI is served by the API process and includes deployment creation, namespace filtering, status summaries, details, and deletion. Its assets are embedded into the Go binary, so there is no separate frontend server.

The manual setup below is available when you want to control each step:

```sh
make build
make test
make kind-up
make docker
make kind-load
kubectl create namespace deploypilot-system
kubectl create namespace demo
kubectl create secret generic deploypilot-api -n deploypilot-system --from-literal=token='replace-me'
make install
```

The checked-in install config is a conservative example. Set allowed namespace and registry values to match the cluster before applying it, and build/tag the image expected by the deployments. Apply CRD first if the API server has not yet established it: `kubectl apply -f config/crd.yaml`, then `kubectl apply -k config/default`.

Start a local API/controller against the current kubeconfig with `API_TOKEN=local ALLOWED_NAMESPACES=demo ALLOWED_REGISTRIES=ghcr.io/example/ go run ./cmd/deploypilot`. `MODE` may be `api`, `controller`, or `all` (default). API listens on `:8082`; controller metrics listen on `:8080`; health probes listen on `:8081`.

## API

See [OpenAPI](api/openapi.yaml). All `/v1` operations require `Authorization: Bearer <API_TOKEN>` when API_TOKEN is set. Create returns `202` only after the CR is persisted; it does not claim the workload is ready. Get and delete require a `namespace` query parameter. List supports `namespace`, `limit` (1–500), and Kubernetes `continue` pagination.

```sh
curl -sS -X POST http://localhost:8082/v1/deployments \
  -H 'Authorization: Bearer local' -H 'Content-Type: application/json' \
  -d '{"name":"hello","namespace":"demo","image":"ghcr.io/example/hello:v1","replicas":2,"port":8080,"readinessProbe":{"path":"/ready"}}'
curl -sS 'http://localhost:8082/v1/deployments/hello?namespace=demo' -H 'Authorization: Bearer local'
curl -sS 'http://localhost:8082/v1/deployments?namespace=demo&limit=100' -H 'Authorization: Bearer local'
curl -i -X DELETE 'http://localhost:8082/v1/deployments/hello?namespace=demo' -H 'Authorization: Bearer local'
```

Errors use `{"error":{"code":"...","message":"...","fields":{...}}}`. Health endpoints are unauthenticated. `/metrics` uses bounded route/method/status labels.

## Installation and operations

Kustomize resources live in `config/`; image publishing uses GitHub Container Registry when a `v*` tag is pushed. Use an immutable release tag in production. Upgrade by applying the new CRD before the controller/API image, then roll the two Deployments; retain the old CRD version until stored requests are migrated. Roll back by restoring the prior image and compatible manifest. Do not remove the CRD to roll back because that deletes stored requests.

Prometheus scrape examples are in `monitoring/prometheus.yaml`; alerts are in `monitoring/alerts.yaml`. The API exposes `/metrics` on port 8082 with bounded route/method/status labels. Import `monitoring/grafana-dashboard.json` into Grafana. Metrics are optional to deployment operation. The workload alert requires kube-state-metrics.

Troubleshooting: inspect `kubectl get deploymentrequests -A`, `kubectl describe deploymentrequest -n <ns> <name>`, and the owned Deployment/Pods/events. For controller downtime, restore its Deployment; persisted CRs resume reconciliation. For repeated errors, inspect controller logs and Kubernetes events and validate registry access, namespace existence, quota, image pull credentials, and probe paths. Deletion is asynchronous until owner-reference garbage collection finishes.

## Checks and release

`make fmt`, `make vet`, `make test`, `make build`, `make manifests`, and `make docker` provide the local checks. GitHub Actions runs format, vet, tests, build, manifest rendering, and image build on PRs. Tagged releases publish `ghcr.io/<owner>/<repo>:<tag>`. Pull request jobs receive no registry credentials.
