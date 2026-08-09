# Static Go binary built with -s -w: nothing left for debuginfo to extract.
%global debug_package %{nil}

Name:           network-doctor
Version:        1.10.8
Release:        1%{?dist}
Summary:        Terminal UI that diagnoses network connectivity and explains where the connection breaks

License:        GPL-3.0-or-later
URL:            https://github.com/heymaikol/network-doctor
Source0:        %{url}/releases/download/v%{version}/%{name}-%{version}.tar.gz
Source1:        %{url}/releases/download/v%{version}/%{name}-%{version}-vendor.tar.gz

BuildRequires:  golang >= 1.25

%description
Network Doctor diagnoses network connectivity and tells you where the
connection breaks in plain English, instead of a wall of tool output. Probes
run unprivileged and time-bounded, and form a dependency graph with
independent branches so an unrelated failure never hides a working path.

The installed binary is named netdoc.

%prep
# -a1 unpacks the vendor tarball after entering the source directory.
%autosetup -a1

%build
export CGO_ENABLED=0
export GOFLAGS="-mod=vendor -trimpath -modcacherw"
# The builder has no network. Fail loudly here rather than mysteriously later.
export GOPROXY=off
export GOTOOLCHAIN=local
go build -ldflags "-s -w -X main.version=%{version}" -o netdoc .

%install
install -Dpm0755 netdoc %{buildroot}%{_bindir}/netdoc

%files
%license LICENSE
%doc README.md
%{_bindir}/netdoc

%changelog
* Sun Aug 09 2026 Michael Placzek <heymaikol@proton.me> - 1.10.8-1
- Update to 1.10.8

* Mon Aug 03 2026 Michael Placzek <heymaikol@proton.me> - 1.10.3-1
- Initial COPR package
