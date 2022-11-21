FROM golang:1.19 AS build
WORKDIR /go/src/github.com/montag451/rlproxy
COPY *.go go.mod go.sum ./
RUN CGO_ENABLED=0 go build

FROM alpine
COPY --from=build /go/src/github.com/montag451/rlproxy/rlproxy .
ENTRYPOINT ["./rlproxy"]
