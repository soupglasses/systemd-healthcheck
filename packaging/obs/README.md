# Open Build Service packaging

These files form the `systemd-healthcheck` package in
`home:soupglasses:systemd-healthcheck/systemd-healthcheck` for openSUSE,
Fedora, RHEL, Debian, Ubuntu, and Arch Linux targets. Copy the directory
contents, including `_service`, to that OBS package's working copy.

Releases use `vMAJOR.MINOR.PATCH` Git tags. The source service deliberately
checks out `@PARENT_TAG@`, so commits after the latest tag are never packaged.
The source archive is created inside the build from that exact tag.

After the package and its distribution repositories exist in OBS, configure an
OBS SCM/CI workflow token and a GitHub webhook using `.obs/workflows.yml` from
this repository. Pull requests then receive isolated OBS test builds. Pushing a
release tag triggers this package's source services and reports every configured
distribution and architecture back to the tagged GitHub commit.

For a manual release update without the webhook:

```console
osc service remoterun home:soupglasses:systemd-healthcheck systemd-healthcheck
```

The source services update every build recipe to the same tag version. The RPM
spec serves openSUSE, Fedora, and RHEL; the DSC metadata serves Debian and
Ubuntu; and `PKGBUILD` serves Arch Linux.
