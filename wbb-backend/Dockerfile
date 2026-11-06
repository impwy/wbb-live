# WBB API server image.
FROM golang:1.27.3-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/wbb-api ./cmd/wbb-api

FROM alpine:3.20
RUN adduser -D -u 10001 wbb && apk add --no-cache ca-certificates
COPY --from=build /out/wbb-api /usr/local/bin/wbb-api
USER wbb
EXPOSE 8080
ENTRYPOINT ["wbb-api"]
