FROM golang:1.26.5-alpine3.24 AS app-build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -o /out/server \
    ./cmd/server

FROM golang:1.26.5-alpine3.24 AS migration-build

RUN CGO_ENABLED=0 GOBIN=/out go install github.com/jackc/tern/v2@v2.4.0

FROM alpine:3.24 AS runtime-base

RUN apk add --no-cache ca-certificates wget \
    && addgroup -S app \
    && adduser -S -G app app

FROM runtime-base AS migration

WORKDIR /migrations

COPY --from=migration-build /out/tern /usr/local/bin/tern
COPY tern.conf ./
COPY migrations ./

USER app

ENTRYPOINT ["/usr/local/bin/tern"]
CMD ["migrate"]

FROM runtime-base AS app

WORKDIR /app

COPY --from=app-build /out/server /app/server

USER app

EXPOSE 8080

ENTRYPOINT ["/app/server"]
