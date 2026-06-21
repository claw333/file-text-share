# syntax=docker/dockerfile:1.7

FROM golang:1.26.4-alpine3.24 AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY *.go *.html *.css *.js favicon.png ./
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/file-text-share \
    .

FROM alpine:3.24

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S app \
    && adduser -S -G app app \
    && mkdir -p /data/uploads \
    && chown -R app:app /data

COPY --from=build /out/file-text-share /usr/local/bin/file-text-share

USER app
WORKDIR /app

EXPOSE 8080
VOLUME ["/data"]

ENTRYPOINT ["/usr/local/bin/file-text-share"]
CMD ["serve"]
