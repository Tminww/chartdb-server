FROM golang:1.23-alpine AS builder

WORKDIR /app

COPY go.mod ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/chartdb-backend ./main.go

FROM alpine:3.21

WORKDIR /app

RUN adduser -D -u 10001 appuser && mkdir -p /data && chown -R appuser:appuser /data

COPY --from=builder /out/chartdb-backend /app/chartdb-backend

ENV PORT=8080
ENV DATA_DIR=/data
ENV MAX_VERSIONS_PER_DIAGRAM=100

EXPOSE 8080

USER appuser
CMD ["/app/chartdb-backend"]
