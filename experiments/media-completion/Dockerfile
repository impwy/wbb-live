FROM golang:1.27.1-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS build
RUN apk add --no-cache git ca-certificates
WORKDIR /src
RUN git clone --depth 1 --branch v1.21.3 https://github.com/bluenviron/mediamtx.git . && \
    test "$(git rev-parse HEAD)" = 9540cb6c1d9f1e9351fcc4ded3afc249ffff22bc
COPY upstream.patch /spike/upstream.patch
RUN git apply --check /spike/upstream.patch && git apply /spike/upstream.patch
RUN printf 'v1.21.3-wbb07a1\n' > internal/core/VERSION && \
    go generate ./internal/servers/hls ./internal/staticsources/rpicamera
RUN CGO_ENABLED=0 go build -o /out/wbb-mediamtx . && \
    CGO_ENABLED=0 go build -o /out/wbb-bootstrap ./cmd/wbb-bootstrap
COPY auth-fixture.go /spike/auth-fixture.go
RUN CGO_ENABLED=0 go build -o /out/wbb-auth-fixture /spike/auth-fixture.go

FROM bluenviron/mediamtx:1.21.3-ffmpeg@sha256:208e9f1740a09a5ee63b6308e77ce0d54fce0456462f640e6bd9acfc62ebb5a2
COPY --from=build /out/wbb-mediamtx /wbb-mediamtx
COPY --from=build /out/wbb-bootstrap /wbb-bootstrap
COPY --from=build /out/wbb-auth-fixture /wbb-auth-fixture
COPY LICENSE /licenses/MediaMTX-LICENSE
ENTRYPOINT ["/wbb-mediamtx"]
