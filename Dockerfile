FROM golang:1.24-bookworm AS builder

WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends \
    git ca-certificates gcc g++ \
    && rm -rf /var/lib/apt/lists/*

COPY . .

RUN go mod tidy && go build -o server ./cmd/server

FROM debian:bookworm-slim

WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /app/server .

EXPOSE 8080

CMD ["./server"]
