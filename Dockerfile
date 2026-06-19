# Build with: docker compose build (requires Docker mirror in China)
# Or run locally without Docker: go run ./cmd/server/

FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /app/server ./cmd/server/

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /app/server /server
EXPOSE 18083
CMD ["/server"]

# If Docker Hub is slow/unreachable, run locally:
#   go run ./cmd/server/
# Or configure Docker mirror:
#   sudo tee /etc/docker/daemon.json <<'EOF'
#   {"registry-mirrors":["https://docker.1ms.run"]}
#   EOF
#   sudo systemctl restart docker
