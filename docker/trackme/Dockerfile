FROM golang:1.24-alpine AS builder

RUN apk add --no-cache build-base libpcap-dev git

RUN git clone https://github.com/pagpeter/TrackMe /trackme

WORKDIR /trackme
RUN go mod download
RUN go build -o /out/trackme ./cmd/main.go

FROM alpine:latest
RUN apk add --no-cache libpcap ca-certificates

WORKDIR /app
COPY --from=builder /out/trackme .
COPY --from=builder /trackme/static ./static/

CMD ["./trackme"]
