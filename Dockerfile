FROM golang:1.16-alpine as builder
WORKDIR /go/src/app
COPY . .
RUN go build -o /app/udp_agent .

FROM alpine:3.13
WORKDIR /app
COPY --from=builder /app/udp_agent .
ENTRYPOINT ["udp_agent"]