# Deploy ABOM

Deployment means installing and configuring ABOM itself. **ABOM is published as
[GitHub Release](https://github.com/advanced-security/codeql-abom/releases) archives, and can be
built from source.** No container image is published: the Helm chart runs an image you build, on
a local kind cluster, or on Amazon EKS by the [runbook](kubernetes.md#on-eks), which has not yet
been run end to end.

```mermaid
flowchart LR
    SOURCE["Build from source"] -->|"source setup"| LOCAL["Local ABOM<br/>API and browser UI"]
    RELEASE["GitHub Release archives"] -->|"release notes"| LOCAL
    HELM["Helm chart"] -->|"image you build"| CLUSTER["Distributed Kubernetes execution"]
```

## Install and configure

| Need | Guide |
| --- | --- |
| Verify, unpack and start a published release archive | That release's [notes](https://github.com/advanced-security/codeql-abom/releases) |
| Install tools, query packs and the local application | [Build from source](source.md) |
| Run the coordinator and worker pools on kind or EKS | [Kubernetes](kubernetes.md) |
| Authenticate, declare ownership and choose paths or optional features | [Configuration](configuration.md) |
| Start, stop, inspect, back up or troubleshoot an installation | [Operations](operations.md) |
| Understand what the resulting evidence means | [Architecture](../architecture.md) and [vocabulary](../vocabulary.md) |

The source guide targets a local GitHub.com installation for an operator responsible for an
organization. Keep the service on loopback. A GitHub credential authorizes GitHub reads; it does
not authenticate people visiting ABOM's web interface.

## Deployment boundaries

The live database is embedded SQLite with a single running owner. Do not place it on shared
network storage or run multiple server/CLI writers against it. Its WAL is part of its committed
state; copying only the main database file is not a backup.

ABOM applies [best-effort remote-source checks](configuration.md#remote-source-eligibility).

The [releases plan](../../plans/tools/releases.md) owns release archives, including the Linux/macOS
amd64/arm64 release matrix, and the [Kubernetes plan](../../plans/tools/kubernetes.md) what stands
between the chart and production. Windows is outside that workstream. Until
qualification is complete, that matrix is not a certification of every operating system,
architecture or external service. Future user guides will accompany a usable Helm route, not
placeholders that suggest that route already exists.
