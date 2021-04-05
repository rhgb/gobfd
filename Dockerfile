FROM golang:1.16-alpine as builder
WORKDIR /go/src/app
COPY . .
RUN go build -v -o /app/agent ./main

FROM alpine:3.13
WORKDIR /app
COPY --from=builder /app/agent .
ENTRYPOINT ["agent"]