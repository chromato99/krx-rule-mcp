# syntax=docker/dockerfile:1
ARG GO_VERSION=1.26.4

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS go-source
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY config ./config

FROM go-source AS go-build
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/krx-rule-mcp ./cmd/krx-rule-mcp
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags "-s -w" -o /out/krx-rule-index ./cmd/krx-rule-index

# Pin the corpus that matches index/current; verify before including either.
# This stage runs natively even when cross-compiling the server for arm64.
FROM go-source AS bundled-data
ARG CORPUS_REVISION=4f3a111f7184737f6e959b50af62f9cc87db412f
ARG CORPUS_ARCHIVE_SHA256=b7911976a022d592ec9b2d384a4283aad71c86afc74c53729f1c730b13e9368a
ADD --checksum=sha256:${CORPUS_ARCHIVE_SHA256} https://codeload.github.com/chromato99/krx-rule-markdown/tar.gz/${CORPUS_REVISION} /tmp/corpus.tar.gz
COPY index ./index
RUN mkdir -p /bundle/index/generations \
    && tar -xzf /tmp/corpus.tar.gz -C /bundle --strip-components=1 "krx-rule-markdown-${CORPUS_REVISION}/data" \
    && go build -o /tmp/check-index ./cmd/krx-rule-index
RUN --network=none /tmp/check-index --data-dir /bundle/data --index-dir /src/index --vector --check --require-full-vector \
    && generation="$(cat index/current)" \
    && cp index/current /bundle/index/current \
    && cp -R "index/generations/${generation}" /bundle/index/generations/ \
    && chmod -R a+rX /bundle

FROM gcr.io/distroless/static-debian12:nonroot AS server
WORKDIR /app
COPY --from=go-build /out/krx-rule-mcp /usr/local/bin/krx-rule-mcp
COPY --from=go-build /out/krx-rule-index /usr/local/bin/krx-rule-index
COPY --from=go-build /src/config /app/config
COPY --from=bundled-data /bundle/data /app/data
COPY --from=bundled-data /bundle/index /app/index
USER nonroot:nonroot
EXPOSE 8080
ENV RULE_MCP_MODE=http
ENV KRX_RULE_DATA_DIR=/app/data
ENV KRX_RULE_INDEX_DIR=/app/index
ENV KRX_DOMAIN_LEXICON_PATH=/app/config/domain-lexicon.yaml
ENTRYPOINT ["/usr/local/bin/krx-rule-mcp"]
