# Build stage
FROM golang:1.26.8 AS builder

WORKDIR /app

# Download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code and build statically linked binary
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o asterisk-dashboard .

# Final stage
FROM alpine:3.21

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

COPY --from=builder /app/asterisk-dashboard /app/asterisk-dashboard

# Default environment variables
ENV HTTP_PORT=8080 \
    AMI_HOST=127.0.0.1 \
    AMI_PORT=5038 \
    AMI_USER=admin \
    AMI_PASS=admin

EXPOSE 8080

ENTRYPOINT ["/app/asterisk-dashboard"]
