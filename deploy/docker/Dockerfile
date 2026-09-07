# QSP container image.
#
# **One static binary on nothing.** The build is CGO_ENABLED=0 and the only
# dependency is a pure-Go SQLite, so the final stage is `scratch`: no shell, no
# package manager, no base image whose CVEs somebody has to track, about 19 MB.
#
# That property is why this file is short, and it is worth keeping. A container
# with an operating system in it is a container somebody has to maintain.

FROM golang:1.27-alpine AS build

WORKDIR /src

# Dependency layer first, so editing source does not re-download the module
# cache. There is one dependency today; this stays correct when there are more.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# VERSION is the file at the repository root, which internal/buildinfo pins a
# test against. Passing it here means `qsp --version` inside the container
# agrees with the tree it was built from — §7 requires that, because systemctl
# reports that something started and not what.
ARG VERSION=development
RUN CGO_ENABLED=0 go build \
      -trimpath \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/qsp \
      ./cmd/qsp

# ---------------------------------------------------------------------------

FROM scratch

# Certificate roots, for the RadioID.net callsign lookups when they are on.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

COPY --from=build /out/qsp /qsp

# **Runs as root, deliberately, and this is the one place that is arguable.**
#
# The image has no /etc/passwd, so an unprivileged user would be a bare numeric
# UID; and the data volume is created by Docker owned by root, so that UID
# could not write the configuration it is supposed to write on first run. The
# usual answer is an entrypoint script that chowns and drops privileges, which
# means abandoning scratch for a base image with a shell — the thing this file
# exists to avoid.
#
# What makes it tolerable: the process binds no port below 1024, the image
# contains one static binary and no shell to escalate into, and the compose
# file uses host networking so there is no capability needed for the socket.
# An operator who wants it unprivileged sets `user:` in the compose file and
# owns the volume themselves.

# Documentation only — the compose file uses host networking, where EXPOSE has
# no effect. Listed so somebody reading the image knows what it binds.
EXPOSE 8080/tcp 62031/udp 50000/udp

# The configuration path is fixed here rather than left to the caller so that
# first-run bootstrap has somewhere to write, and so that the volume mount in
# the compose file and this path cannot drift apart.
ENTRYPOINT ["/qsp", "-config", "/var/lib/qsp/qsp.json"]
