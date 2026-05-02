# syntax=docker/dockerfile:1

FROM golang:1.23-alpine AS builder
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags='-s -w' -o /out/dpg-pay ./cmd/server

FROM alpine:3.21 AS runtime
WORKDIR /app

RUN adduser -D -u 10001 dpgpay

COPY --from=builder /out/dpg-pay /app/dpg-pay
COPY internal/templates /app/internal/templates
COPY static /app/static
COPY migrations /app/migrations

USER dpgpay
EXPOSE 18231

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD wget -qO- http://localhost:18231/health || exit 1

ENTRYPOINT ["/app/dpg-pay"]
