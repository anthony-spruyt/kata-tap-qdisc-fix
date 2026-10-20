# renovate: depName=golang datasource=docker
ARG GO_VERSION=1.27.2
FROM golang:${GO_VERSION}-alpine AS builder

ARG VERSION=dev
ARG COMMIT=unknown

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
  -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
  -o /kata-tap-qdisc-fix ./cmd/kata-tap-qdisc-fix

FROM gcr.io/distroless/static:nonroot@sha256:516757106de35205efb70b91ba03f6f1298424a0678c603255cf2852a7000087
COPY --from=builder /kata-tap-qdisc-fix /kata-tap-qdisc-fix
USER 65534:65534
# Health monitored via Kubernetes liveness/readiness probes, not docker healthcheck.
HEALTHCHECK NONE
ENTRYPOINT ["/kata-tap-qdisc-fix"]
