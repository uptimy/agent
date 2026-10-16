# Kubernetes

## Install with Helm

```bash
helm install uptimy-agent oci://ghcr.io/uptimy/charts/uptimy-agent -n monitoring --create-namespace
kubectl -n monitoring port-forward svc/uptimy-agent 8080:80
```

The chart is released with the agent, at the same version, and signed with cosign. Its values are documented in the [chart README](../deploy/helm/uptimy-agent/README.md).

Inside a cluster the agent uses its service account to check Deployments, StatefulSets, DaemonSets and Services. The chart creates a read-only role for that. Monitors can live in `values.yaml`:

```yaml
healthchecks:
  - name: Checkout API
    type: http
    target: http://checkout.shop.svc.cluster.local:8080/health
    interval: 30s
  - name: Checkout deployment
    type: kubernetes
    target: shop/deployment/checkout
heartbeats:
  - name: Nightly backup
    cron: "0 3 * * *"
    timezone: Europe/Berlin
```

## Auto-discovery

Label a Service, Ingress, Gateway API HTTPRoute, Deployment, StatefulSet or DaemonSet with `upti.my/monitor: "true"` and the agent creates a monitor for it within 30 seconds, so a new app is monitored as soon as it's deployed:

| Resource | Monitor | Default name |
| --- | --- | --- |
| Service | HTTP check on `http://<name>.<namespace>.svc:<port>` with the path (and scheme) of its pods' readinessProbe, when they have one on that port, otherwise `/`. Ports with no HTTP readinessProbe that don't look like HTTP (named `http`, `https`, `web` or `http-…`, an HTTP `appProtocol`, or port 80, 443 or 8080) get a TCP connect | `<namespace>/<name>` |
| Ingress | HTTP check per hostname, over HTTPS when the Ingress has TLS for it | the hostname |
| HTTPRoute | HTTPS check per hostname | the hostname |
| Deployment, StatefulSet, DaemonSet | readiness check: all replicas ready | `<namespace>/<name> (<kind>)` |
| CronJob | heartbeat on its schedule and time zone, with runs read from its Jobs (see below) | `<namespace>/<name> (cronjob)` |

```yaml
apiVersion: v1
kind: Service
metadata:
  name: checkout
  namespace: shop
  labels:
    upti.my/monitor: "true"
  annotations:
    upti.my/name: Checkout API
    upti.my/path: /health
```

By default the agent checks a Service with its own request, end to end through DNS, the Service and the app, as described above. With `upti.my/type: kubernetes` it reuses the kubelet's readinessProbes instead: the Service is up while it has a ready endpoint, and the agent sends the app no traffic. That works on any port and protocol, but only knows what the readinessProbe tests. You can also add one by hand: a Kubernetes healthcheck on `<namespace>/service/<name>`.

Annotations adjust what's checked:

| Annotation | Applies to | Default |
| --- | --- | --- |
| `upti.my/name` | all | see above |
| `upti.my/path` | Service, Ingress, HTTPRoute | a Service's readinessProbe path, else `/`. On a Service it also makes the check HTTP |
| `upti.my/port` | Service | the first port (a name or a number) |
| `upti.my/type` | Service | `http`, `tcp` or `kubernetes`; picked as above |
| `upti.my/scheme` | Service, Ingress, HTTPRoute | `http` or `https`, picked as above |
| `upti.my/interval` | all but CronJobs | `1m` (e.g. `30s`) |
| `upti.my/grace` | CronJob | the Job's `activeDeadlineSeconds` plus a minute, or else the time between runs, at most `1h` |
| `upti.my/expected-status` | HTTP checks | `200-399` |
| `upti.my/keyword` | HTTP checks | – |
| `upti.my/failure-threshold` | all but CronJobs | `2` failed checks in a row before it's down (1–10) |
| `upti.my/recovery-threshold` | all but CronJobs | `1` good check in a row before it's up again (1–10) |

**CronJobs need no ping.** The agent reads each discovered CronJob's Jobs every 10 seconds and records every run at the times Kubernetes saw: when the Job started, when it completed or failed, and for a failure the reason with the container's exit code (`BackoffLimitExceeded; container backup exited with code 137 (OOMKilled)`). Missed runs come from the schedule as for any heartbeat. A run must finish within the grace period after it's due, so the default grace leaves room for the job itself. Suspending the CronJob pauses its heartbeat, and resuming it resumes it.

Discovered monitors are read-only in the UI, marked "Discovered in Kubernetes" with the object they came from, and follow the resource: changing an annotation updates the monitor (renaming it with `upti.my/name` keeps its history), and removing the label or the resource deletes it. `true`, `yes`, `1` and `on` opt a resource in; `false`, `no`, `0` and `off` opt it out explicitly; any other value is reported.

**Settings → Kubernetes** shows how discovery is doing (last scan, scope, how many monitors) and anything it couldn't use, like a mistyped label value or an invalid annotation, so a resource that doesn't show up says why. It also lists the cluster's Services, Ingresses, HTTPRoutes, workloads and CronJobs, monitored or not, with the `kubectl label` command for each one or for everything shown. The agent itself never changes the cluster: labeling stays with you and your manifests. You can still pause them and add them to the status page. Catch-all and wildcard hostnames are skipped. With `rbac.clusterWide: false` the agent only looks in its own namespace; set `discovery.enabled: false` in the chart (or `KUBERNETES_DISCOVERY=false`) to turn it off.
