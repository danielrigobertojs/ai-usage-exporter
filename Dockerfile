# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" go build -trimpath \
    -ldflags="-s -w -X github.com/danielrigobertojs/ai-usage-exporter/internal/version.Version=$VERSION -X github.com/danielrigobertojs/ai-usage-exporter/internal/version.Commit=$COMMIT -X github.com/danielrigobertojs/ai-usage-exporter/internal/version.BuildDate=$BUILD_DATE" \
    -o /out/ai-usage-exporter ./cmd/ai-usage-exporter

FROM scratch

# TLS is needed only when the optional models.dev catalog refresh is enabled.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/ai-usage-exporter /ai-usage-exporter

USER 65532:65532
ENTRYPOINT ["/ai-usage-exporter"]
CMD ["serve"]
