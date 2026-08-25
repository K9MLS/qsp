# QSP container image.
#
# The build is fully static (CGO_ENABLED=0) so that the final stage can be
# scratch: no shell, no package manager, no base image CVEs to track.

FROM golang:1.22-alpine AS build

WORKDIR /src

# go.mod is copied first so that the dependency layer is cached independently of
# source changes. The core has no dependencies today (ADR-0004), which makes
# this cheap rather than pointless — it stays correct when that changes.
COPY go.mod ./
RUN go mod download

COPY . .

ARG VERSION=development
RUN CGO_ENABLED=0 go build \
      -trimpath \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/qsp \
      ./cmd/qsp

# ---------------------------------------------------------------------------

FROM scratch

# Certificate roots, for outbound TLS when connectors need it.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/qsp /qsp

# Run unprivileged. The numeric UID avoids needing /etc/passwd in scratch.
USER 65532:65532

EXPOSE 8080

ENTRYPOINT ["/qsp"]
