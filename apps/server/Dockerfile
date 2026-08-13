FROM golang:1.26.1-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY apps/server ./apps/server

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/server ./apps/server/cmd/server
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/migrate ./apps/server/cmd/migrate
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/agent-runtime-ops ./apps/server/cmd/agent-runtime-ops

FROM alpine:3.20

RUN adduser -D -u 10001 appuser

WORKDIR /app

COPY --from=builder /out/server ./server
COPY --from=builder /out/migrate ./migrate
COPY --from=builder /out/agent-runtime-ops ./agent-runtime-ops

EXPOSE 8080

USER appuser

ENTRYPOINT ["./server"]
