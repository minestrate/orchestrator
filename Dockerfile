# Build stage
FROM golang:1.26-alpine AS builder

WORKDIR /app

# Download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o minestrate .

# Run stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /app

# Copy the binary from builder
COPY --from=builder /app/minestrate .

# Expose HTTP port
EXPOSE 8080

ENTRYPOINT ["./minestrate"]
CMD ["--config", "/app/config/minestrate.yaml"]
