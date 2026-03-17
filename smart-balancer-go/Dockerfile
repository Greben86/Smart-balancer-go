FROM golang:1.24-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY main.go .

# Build the binary
RUN go build -o smart-balancer .

# Final stage
FROM alpine:latest as run

WORKDIR /root/

# Copy the binary from builder stage
COPY --from=builder /app/smart-balancer /root/smart-balancer

EXPOSE 8080
EXPOSE 9090

CMD ["/root/smart-balancer", "--backends=http://backend1:8080,http://backend2:8080", "--port=:8080"]