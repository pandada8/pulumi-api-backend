FROM golang:1.26.5-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY third_party/pulumi/pkg/go.mod third_party/pulumi/pkg/go.sum third_party/pulumi/pkg/
COPY third_party/pulumi/sdk/go.mod third_party/pulumi/sdk/go.sum third_party/pulumi/sdk/
RUN go mod download
COPY cmd cmd
COPY internal internal
COPY third_party/pulumi/pkg third_party/pulumi/pkg
COPY third_party/pulumi/sdk third_party/pulumi/sdk
RUN CGO_ENABLED=0 go build -ldflags '-X github.com/pulumi/pulumi/sdk/v3/go/common/version.Version=3.246.0' -o /out/backend ./cmd/backend && CGO_ENABLED=0 go build -o /out/backendctl ./cmd/backendctl
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/backend /out/backendctl /app/
USER 65532:65532
WORKDIR /app
ENTRYPOINT ["/app/backend"]
CMD ["serve"]
