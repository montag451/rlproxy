FROM golang:1.18 AS build
WORKDIR /go/src/github.com/montag451/rlproxy
COPY main.go go.* ./
RUN CGO_ENABLED=0 go build

FROM alpine
COPY --from=build /go/src/github.com/montag451/rlproxy/rlproxy .
ENTRYPOINT ["./rlproxy"]