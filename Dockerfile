FROM golang:1.15.5 as builder
WORKDIR /go/src/app
ADD . /go/src/app/
RUN GOOS=linux GOARCH=amd64 go build

# Now copy it into our base image.
FROM gcr.io/distroless/base-debian10
COPY --from=builder /go/src/app/traefik-auth-forwarder /
CMD ["/traefik-auth-forwarder"]