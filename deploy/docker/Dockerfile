ARG GO_VERSION=1.26.4

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/krx-rule-mcp ./cmd/krx-rule-mcp
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags "-s -w" -o /out/krx-rule-index ./cmd/krx-rule-index

FROM gcr.io/distroless/static-debian12:nonroot AS server
WORKDIR /app
COPY --from=go-build /out/krx-rule-mcp /usr/local/bin/krx-rule-mcp
COPY --from=go-build /out/krx-rule-index /usr/local/bin/krx-rule-index
COPY --from=go-build /src/config /app/config
USER nonroot:nonroot
EXPOSE 8080
ENV RULE_MCP_MODE=http
ENV KRX_RULE_DATA_DIR=/app/data
ENV KRX_RULE_INDEX_DIR=/app/index
ENV KRX_DOMAIN_LEXICON_PATH=/app/config/domain-lexicon.yaml
ENTRYPOINT ["/usr/local/bin/krx-rule-mcp"]
