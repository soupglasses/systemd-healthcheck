# sd-healthcheck

[![CI](https://github.com/soupglasses/systemd_healthcheck/actions/workflows/ci.yml/badge.svg)](https://github.com/soupglasses/systemd_healthcheck/actions/workflows/ci.yml)
[![OBS build](https://build.opensuse.org/projects/home:soupglasses:systemd-healthcheck/packages/systemd-healthcheck/badge.svg?type=percent)](https://build.opensuse.org/package/show/home:soupglasses:systemd-healthcheck/systemd-healthcheck)

`sd-healthcheck` makes a container-style health-check command usable as a
systemd readiness and watchdog source. It is a small foreground supervisor
written in Go with no third-party packages.

The health check is deliberately protocol-agnostic. Use `curl` for HTTP,
`grpc_health_probe` for gRPC, `nc` for TCP, or a service-specific executable.
An exit status of zero means healthy; any other result means unhealthy.

## Installation

**[Interactive Download][package-downloads]**

Packages are built and published by the openSUSE Build Service for:

| Distribution | Releases | Architectures |
| --- | --- | --- |
| openSUSE Tumbleweed | Rolling | `i586`, `x86_64`, `aarch64` |
| openSUSE Leap | 16.0 | `x86_64`, `aarch64` |
| Fedora | 43, 44 | `x86_64`, `aarch64` |
| Debian | 13 | `x86_64`, `aarch64` |
| Ubuntu | 24.04 LTS, 26.04 LTS | `x86_64`, `aarch64` |
| Arch Linux | Rolling | `x86_64` |

### Manual installation with Go

If your distribution is not listed, Go 1.22 or newer can build and install the
current release:

```console
CGO_ENABLED=0 go install -trimpath -ldflags="-X main.version=1.0.0" \
  github.com/soupglasses/systemd_healthcheck/cmd/sd-healthcheck@v1.0.0
```

This is a fallback and is not recommended for managed systems. It installs
only the binary into `GOBIN` or `GOPATH/bin`; it does not provide a signed
distribution package, automatic package-manager updates, or package-manager
ownership. Prefer the interactive download when a package is available.

[package-downloads]: https://software.opensuse.org/download.html?project=home%3Asoupglasses%3Asystemd-healthcheck&package=systemd-healthcheck

## Service unit

```systemd
[Unit]
Description=Example HTTP service
After=network.target

[Service]
Type=notify
NotifyAccess=main
KillMode=mixed
WatchdogSec=30s
TimeoutStartSec=2min
Restart=on-failure

Environment="SD_HEALTHCHECK_CMD=/usr/bin/curl --fail --silent --show-error --max-time 2 http://127.0.0.1:3000/healthz"
ExecStart=/usr/local/bin/sd-healthcheck /usr/local/bin/example-server --port 3000

[Install]
WantedBy=multi-user.target
```

Keeping the health command in `Environment=` leaves `ExecStart=` and
`systemctl status` focused on the supervised service. For a long command, put
the same assignment in an `EnvironmentFile=` instead. Do not put secrets in
the command; have the health-check executable read them from a protected file
or a systemd credential.

`SD_HEALTHCHECK_CMD` uses `/bin/sh -c`, matching the shell form of Docker's
`HEALTHCHECK CMD`. Configuration is environment-only, so the first argument is
always the service executable and no `--` separator is needed. An optional
leading `--` is accepted for people who prefer the visual boundary.

## Podman and packaged services

Containers managed by Podman already have this facility through Quadlet. Use
Podman's health check directly instead of wrapping the container:

```systemd
[Container]
Image=example/image
Notify=healthy
HealthCmd=/usr/local/bin/healthcheck
HealthInterval=10s
HealthTimeout=3s
HealthRetries=3
HealthOnFailure=kill

[Service]
Restart=on-failure
```

`Notify=healthy` delays service readiness until the container is healthy, and
`HealthOnFailure=kill` lets systemd's restart policy handle runtime failure.
See the [Podman Quadlet documentation][quadlet-health].

`sd-healthcheck` provides the equivalent command-based option for native
packages. A package can ship a dedicated check in `/usr/libexec` and wrap its
ordinary daemon without adding container machinery:

```systemd
[Service]
Environment="SD_HEALTHCHECK_CMD=/usr/libexec/example-healthcheck"
ExecStart=/usr/bin/sd-healthcheck /usr/sbin/example-daemon
```

[quadlet-health]: https://docs.podman.io/en/latest/markdown/podman-systemd.unit.5.html#healthonfailure

## Behavior

The wrapper starts the service and immediately runs the health command:

1. Before the first success, failed checks update `STATUS=` and are retried.
   The first success sends `READY=1` and `WATCHDOG=1` together.
2. After readiness, checks run every half of the interval supplied by systemd
   in `WATCHDOG_USEC`. A check may run for at most one quarter of the watchdog
   interval.
3. A successful check sends `WATCHDOG=1`. A failed check updates `STATUS=` and
   withholds the watchdog ping, retrying after one tenth of the watchdog
   interval. If health does not recover, systemd reaches `WatchdogSec=` and
   applies its configured watchdog action.
4. The wrapper exits when the service exits and preserves a non-zero child exit
   code. Stop signals are forwarded to the service process group.

Health command output is streamed directly to the wrapper's standard output and
error. Keep successful checks quiet to avoid unnecessary journal traffic.

There is intentionally no independent retry threshold or restart policy in the
wrapper. `TimeoutStartSec=`, `WatchdogSec=`, and `Restart=` remain the single
source of truth in the service unit. Watchdog intervals below 100 milliseconds
are rejected to avoid repeatedly starting and killing probes on an unrealistically
tight schedule.

The wrapper owns the notify protocol. It does not pass `NOTIFY_SOCKET`,
`WATCHDOG_USEC`, `WATCHDOG_PID`, or `SD_HEALTHCHECK_*` variables to the service
or health-check subprocesses. `KillMode=mixed` makes systemd send the initial
stop signal to the wrapper, which forwards it to the service process group. If
shutdown times out, systemd still sends the final kill signal to every remaining
process in the unit's control group.

## Build and test

Go 1.22 or newer is required.

```console
go build -trimpath -o sd-healthcheck ./cmd/sd-healthcheck
go test -race ./...
go vet ./...
```

The tests exercise the shell health check, supervisor lifecycle, watchdog
notification behavior, filesystem and Linux abstract Unix datagram sockets,
and an end-to-end wrapper run with a real notification socket.

## Acknowledgements

This project was inspired by
[jhass/systemd_http_health_check](https://github.com/jhass/systemd_http_health_check)
and is a ground-up Go implementation with a command-based health-check model.

## License

BSD 3-Clause. See [LICENSE](LICENSE).
