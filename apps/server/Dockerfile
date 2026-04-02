FROM golang:1.26.1-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY apps/server ./apps/server

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/server ./apps/server/cmd/server

FROM alpine:3.20

RUN adduser -D -u 10001 appuser

WORKDIR /app

COPY --from=builder /out/server ./server

EXPOSE 8080

USER appuser

ENTRYPOINT ["./server"]
