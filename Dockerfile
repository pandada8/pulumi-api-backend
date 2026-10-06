FROM golang:1.26.5-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY third_party/pulumi/pkg/go.mod third_party/pulumi/pkg/go.sum third_party/pulumi/pkg/
COPY third_party/pulumi/sdk/go.mod third_party/pulumi/sdk/go.sum third_party/pulumi/sdk/
RUN go mod download
COPY cmd cmd
COPY internal internal
COPY LICENSE THIRD_PARTY_NOTICES.md ./
COPY third_party/pulumi/LICENSE third_party/pulumi/LICENSE
COPY third_party/pulumi/pkg third_party/pulumi/pkg
COPY third_party/pulumi/sdk third_party/pulumi/sdk
RUN go install github.com/google/go-licenses/v2@v2.0.1
# The Pulumi monorepo keeps its license above both Go module roots. The
# collector stops at a module root, so expose that unchanged license in each.
RUN cp third_party/pulumi/LICENSE third_party/pulumi/pkg/LICENSE && \
    cp third_party/pulumi/LICENSE third_party/pulumi/sdk/LICENSE && \
    CGO_ENABLED=0 go-licenses save ./cmd/backend ./cmd/backendctl --save_path=/out/licenses/dependencies && \
    cp LICENSE THIRD_PARTY_NOTICES.md /out/licenses/ && \
    cp /usr/local/go/LICENSE /out/licenses/GO-LICENSE && \
    cp /usr/local/go/PATENTS /out/licenses/GO-PATENTS
RUN CGO_ENABLED=0 go build -ldflags '-X github.com/pulumi/pulumi/sdk/v3/go/common/version.Version=3.246.0' -o /out/backend ./cmd/backend && CGO_ENABLED=0 go build -o /out/backendctl ./cmd/backendctl
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/backend /out/backendctl /app/
COPY --from=build /out/licenses /usr/share/licenses/pulumid/
LABEL org.opencontainers.image.licenses="Apache-2.0"
USER 65532:65532
WORKDIR /app
ENTRYPOINT ["/app/backend"]
CMD ["serve"]
