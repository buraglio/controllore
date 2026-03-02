FROM golang:1.21-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o pced ./cmd/pced
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o controllore ./cmd/cli

FROM alpine:3.19
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /build/pced /build/controllore /app/
EXPOSE 8080 4189
ENTRYPOINT ["/app/pced"]
CMD ["--config", "/etc/controllore/controllore.yaml"]
