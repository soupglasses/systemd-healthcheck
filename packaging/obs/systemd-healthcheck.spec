Name:           systemd-healthcheck
Version:        0
Release:        0
Summary:        Command-based systemd readiness and watchdog supervisor
License:        BSD-3-Clause
URL:            https://github.com/soupglasses/systemd_healthcheck
Source0:        systemd_healthcheck-%{version}.tar.xz

%if 0%{?suse_version}
BuildRequires:  golang(API) >= 1.23
%else
BuildRequires:  golang >= 1.23
%endif

%description
sd-healthcheck wraps a service process and turns a container-style health-check
command into systemd readiness and watchdog notifications. It implements the
notification protocol directly and does not depend on libsystemd.

%prep
%autosetup -n systemd_healthcheck-%{version}

%build
export CGO_ENABLED=0
go build -buildvcs=false -mod=readonly -trimpath \
  -ldflags "-s -w -X main.version=%{version}" \
  -o sd-healthcheck ./cmd/sd-healthcheck

%check
go test -buildvcs=false -mod=readonly ./...

%install
install -Dpm0755 sd-healthcheck %{buildroot}%{_bindir}/sd-healthcheck

%files
%license LICENSE
%doc README.md
%{_bindir}/sd-healthcheck

%changelog
