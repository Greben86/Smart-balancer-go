FROM golang:1.23-alpine AS builder

WORKDIR /app

COPY go.mod .
RUN go mod download

COPY main.go .
RUN go build -o client main.go

FROM alpine:latest
RUN apk --no-cache add ca-certificates
COPY --from=builder /app/client /client
CMD ["/client"]