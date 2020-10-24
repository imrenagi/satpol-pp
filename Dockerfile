FROM golang:latest as builder
RUN mkdir -p /satpol-pp
WORKDIR /satpol-pp
COPY . .
RUN make build.binaries

FROM alpine:3.10
WORKDIR /
RUN apk update && apk add --no-cache ca-certificates tzdata && update-ca-certificates
COPY --from=builder /satpol-pp/bin/satpol-pp .

CMD ["./satpol-pp", "server"]
