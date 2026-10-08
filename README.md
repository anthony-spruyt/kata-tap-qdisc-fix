# kata-tap-qdisc-fix

DaemonSet that fixes a Cilium ↔ Kata networking regression caused by Linux kernel ≥6.18 defaulting `tap` devices to the `fq` qdisc whose `horizon_drop` silently drops Cilium-timestamped reply packets mirrored into the Kata VM via `tc mirred`.

The daemon walks `/proc/*/ns/net`, deduplicates by netns inode, and for every `tap[0-9]+_kata` interface in any container netns whose root qdisc is `fq`, replaces it with `pfifo_fast`. This approach finds taps that live in cloud-hypervisor's orphan netns, which `/run/netns` never surfaces. Runs only on nodes labelled `kata.spruyt-labs/ready=true`.

## Reconciliation model

Single proc-sweep loop — no fsnotify, no retry state:

1. **Initial sweep** on startup — walk `/proc/*/ns/net` once.
2. **Periodic full sweep every `SWEEP_INTERVAL` seconds** — the authoritative correctness path. Transient errors (process exited mid-sweep, netns disappears) are logged at debug level and silently retried on the next sweep.

Deduplication is by netns inode, so shared-netns pods are visited exactly once regardless of how many `/proc/<pid>/ns/net` symlinks point to the same netns. The host netns (inode of `/proc/1/ns/net`) is always excluded.

## Layout

- `cmd/kata-tap-qdisc-fix`: the daemon (the image entrypoint, `/kata-tap-qdisc-fix`)
- `cmd/proc-scanner`: one-shot diagnostic that runs a single sweep and prints the counts; not in the image
- `internal/procscan`: `/proc` walk, netns dedup and sweep
- `internal/qdisc`: tap detection and the `fq` to `pfifo_fast` replacement over netlink
- `internal/netns`: entering a netns on a locked OS thread and restoring the host netns
- `internal/config`, `internal/metrics`, `internal/server`: environment, Prometheus counters, health and metrics servers

```bash
go build ./...
go test -race ./...
golangci-lint run
```

## Run locally (requires CAP_SYS_ADMIN + CAP_NET_ADMIN)

```bash
sudo ./kata-tap-qdisc-fix
```

## Environment

| Var              | Default | Meaning                                                |
| ---------------- | ------- | ------------------------------------------------------ |
| `DRY_RUN`        | `false` | If `true`, log intended replacements without executing |
| `HEALTH_PORT`    | `8080`  | Port for `/healthz` and `/readyz`                      |
| `METRICS_PORT`   | `9102`  | Port for `/metrics` (Prometheus)                       |
| `LOG_LEVEL`      | `info`  | `debug`, `info`, `warn`, `error`                       |
| `SWEEP_INTERVAL` | `30`    | Seconds between periodic full sweeps                   |

## Canary & Rollback

For initial deploy or risky upgrades, flip the DaemonSet to dry-run mode first and watch metrics for at least 1h before enforcing.

1. Patch values: set `env.DRY_RUN` to `"true"` in `cluster/apps/kube-system/kata-tap-qdisc-fix/app/values.yaml`, commit, push. Flux reconciles.
2. Observe: `kata_tap_qdisc_replacements_total` stays at 0 (enforced side-effect disabled), but logs show `"qdisc would replace (dry-run)"` lines for every Kata pod that would have been patched. Confirm the `path` log fields match real Kata pods.
3. Flip `DRY_RUN` back to `"false"`, commit, push.

Emergency rollback:

```bash
flux -n flux-system suspend hr kata-tap-qdisc-fix
# Or — revert the Flux Kustomization entirely:
git revert <cluster-manifest-commit-range>
git push origin main
```

Alerting (PromQL):

- `increase(kata_tap_qdisc_replace_failures_total[10m]) > 0` — WARN

## Releases

release-please opens a release PR from conventional commits. Merging it tags `vX.Y.Z` and creates a draft release. The image `ghcr.io/anthony-spruyt/kata-tap-qdisc-fix` is then built from the tag and pushed with an SBOM and provenance. The release is published only after the push succeeds.

If the image job fails, the release stays a draft. Fix the cause, then run the **Rebuild Release** workflow with the version.

Releases up to 0.2.14 were cut from [spruyt-labs](https://github.com/anthony-spruyt/spruyt-labs) as `kata-tap-qdisc-fix/vX.Y.Z`. Releases from this repo start at 1.0.0.

## Root cause reference

See issue anthony-spruyt/spruyt-labs#951 for the packet-trace evidence and anthony-spruyt/spruyt-labs#959 for the proc-enumeration spike results.

Merge gate smoke test line; safe to remove.
