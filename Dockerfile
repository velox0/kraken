FROM golang:1.25-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o /out/kraken ./cmd/app
RUN go build -o /out/useradmin ./cmd/useradmin

FROM alpine:3.21

RUN apk add --no-cache bash ca-certificates tzdata

WORKDIR /app

COPY --from=builder /out/kraken /app/kraken
COPY --from=builder /out/useradmin /app/useradmin
COPY scripts/fixes /app/scripts/fixes
COPY docker-entrypoint.sh /app/docker-entrypoint.sh

RUN chmod +x /app/docker-entrypoint.sh

EXPOSE 8080

ENTRYPOINT ["/app/docker-entrypoint.sh"]
