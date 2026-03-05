# Smart Balancer

High-performance Go-based microservice for load balancing requests between backend services using the round-robin method.

## Features

- **Round-robin load balancing** – evenly distributes incoming requests across multiple backend services.
- **Request counting** – tracks the number of requests sent to each backend.
- **Prometheus metrics** – exposes detailed metrics for monitoring:
  - `smart_balancer_requests_total` – total number of requests per backend.
  - `smart_balancer_request_duration_seconds` – request duration histogram per backend.
- **Health check endpoint** – `/health` returns service status and backend list.
- **Reverse proxy behavior** – forwards all unmatched routes to backends with full header and body passthrough.
- **Configurable backends and port** – supports CLI flags for dynamic configuration.

## Performance

Built with **Gin** (high-performance HTTP web framework) and standard `net/http` client for minimal overhead.
Utilizes atomic operations and efficient concurrency patterns to ensure thread-safe counter updates.

## Usage

```bash
# Build
go build -o smart-balancer

# Run with custom backends and port
./smart-balancer --backends="http://service1:8080,http://service2:8080" --port=:8080

# Default values
./smart-balancer  # uses http://backend1:8080,http://backend2:8080 on :8080
```

## Metrics

Prometheus metrics are exposed on `:9090/metrics` by default.

## Docker

A `Dockerfile` is included for containerized deployment.

## License

MIT