FROM golang:1.19 AS build
ARG version=unknown
WORKDIR /go/src/github.com/montag451/rlproxy
COPY . .
RUN CGO_ENABLED=0 make VERSION="${version}"

FROM alpine
COPY --from=build /go/src/github.com/montag451/rlproxy/.build/rlproxy .
ENTRYPOINT ["./rlproxy"]
