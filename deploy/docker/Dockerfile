# QSP container image.
#
# **One static binary on nothing.** The build is CGO_ENABLED=0 and the only
# dependency is a pure-Go SQLite, so the final stage is `scratch`: no shell, no
# package manager, no base image whose CVEs somebody has to track, about 19 MB.
#
# That property is why this file is short, and it is worth keeping. A container
# with an operating system in it is a container somebody has to maintain.

# **Built on the machine's own platform, for the target's.** The build stage
# runs natively and Go cross-compiles, so the arm64 image costs seconds rather
# than the minutes QEMU emulation takes. BUILDPLATFORM, TARGETOS and TARGETARCH
# are supplied by buildx; a plain `docker build` supplies them for the host.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

# **git, so the binary can name its own commit.**
#
# Go stamps the revision into the build info from the repository it compiles
# in, and silently omits it when git is missing — which is why the container
# reported "(development build)" while the same source built on a workstation
# reported a pseudo-version naming the commit. §7's rule is to check the
# running binary after every deploy, and an image that cannot say which commit
# it came from cannot be checked. It is dropped again with the build stage, so
# the final image is still scratch.
RUN apk add --no-cache git

# Dependency layer first, so editing source does not re-download the module
# cache. There is one dependency today; this stays correct when there are more.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# **The version is read from the tree, not passed in.**
#
# This was an ARG with a default of "development", and the compose override
# that was supposed to supply the real value passed the literal string
# "development" — directly beneath a comment claiming it kept the version
# honest. So every container ever built reported `development (development
# build)`, and §7's rule to check the running binary after every deploy had no
# working implementation on that machine. An afternoon went on a container that
# had not been rebuilt, diagnosed with a version string that could not have
# said so.
#
# VERSION is copied in with the source above, so reading it here needs no
# argument, no override, and nothing to keep in sync. A value derived from the
# tree cannot drift from it.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
      -trimpath \
      -ldflags="-s -w -X main.version=$(cat VERSION)" \
      -o /out/qsp \
      ./cmd/qsp
# The directories the unprivileged user writes, made here because scratch has
# no mkdir. Copied into the final image owned by that user; see below.
RUN mkdir -p /out/var/lib/qsp /out/run/qsp

# ---------------------------------------------------------------------------

FROM scratch

# Certificate roots, for the RadioID.net callsign lookups when they are on.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

COPY --from=build /out/qsp /qsp

# **Runs as UID 65532, not root** (0412). This file used to run as root, and
# said why: the data volume "is created by Docker owned by root, so that UID
# could not write the configuration". That holds for an empty directory. It does
# not hold when the image already has /var/lib/qsp owned by the user — Docker
# copies a directory's ownership into a fresh named volume the first time it is
# mounted — so no entrypoint script and no shell are needed, and the image
# stays scratch.
#
# **An existing volume keeps its root ownership**, so an install from before
# 0.1.254 needs one chown before upgrading; deploy/docker/README.md has it.
# 65532 is the conventional unprivileged ID of distroless images. The logon
# socket for qsp-zello lives in /run/qsp, so that is owned too.
COPY --from=build --chown=65532:65532 /out/var/lib/qsp /var/lib/qsp
COPY --from=build --chown=65532:65532 /out/run/qsp /run/qsp
USER 65532:65532
# Documentation only — the compose file uses host networking, where EXPOSE has
# no effect. Listed so somebody reading the image knows what it binds.
EXPOSE 8080/tcp 62031/udp 50000/udp

# The configuration path is fixed here rather than left to the caller so that
# first-run bootstrap has somewhere to write, and so that the volume mount in
# the compose file and this path cannot drift apart.
ENTRYPOINT ["/qsp", "-config", "/var/lib/qsp/qsp.json"]
