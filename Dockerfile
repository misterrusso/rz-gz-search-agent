FROM golang:1.26-alpine AS builder
WORKDIR /app

COPY go.mod go.sum* ./
COPY third_party ./third_party
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /bin/server ./cmd/server

FROM alpine:3.20
WORKDIR /app
COPY --from=builder /bin/server /app/server

EXPOSE 8080
CMD ["/app/server"]