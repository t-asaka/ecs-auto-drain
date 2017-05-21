FROM golang:1.8.0-alpine

RUN \
  apk update && \
  apk add --virtual .build \
    git \
    make && \
  go get -u github.com/golang/dep/...

WORKDIR /go/src/github.com/t-asaka/ecs-auto-drain

ADD . /go/src/github.com/t-asaka/ecs-auto-drain
RUN make deps && make
