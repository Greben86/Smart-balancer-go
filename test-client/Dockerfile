FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod ./

RUN go mod download

COPY main.go .

RUN go build -o client .

FROM alpine:latest

RUN apk --no-cache add ca-certificates

COPY --from=builder /app/client /client

CMD ["/client"]