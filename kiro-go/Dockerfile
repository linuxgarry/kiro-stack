FROM golang:1.22-alpine AS builder

RUN apk --no-cache add build-base git

WORKDIR /app
COPY . .
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -o kiro-api-proxy .

FROM alpine:latest
RUN apk --no-cache add ca-certificates

WORKDIR /app
COPY --from=builder /app/kiro-api-proxy .
COPY --from=builder /app/web ./web

EXPOSE 8080
VOLUME /app/data

CMD ["./kiro-api-proxy"]
