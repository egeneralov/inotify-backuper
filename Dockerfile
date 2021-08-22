FROM golang:1.16.5

ENV \
  GO111MODULE=on \
  CGO_ENABLED=0 \
  GOOS=linux \
  GOARCH=amd64

WORKDIR /go/src/github.com/egeneralov/inotify-backuper
ADD go.mod go.sum /go/src/github.com/egeneralov/inotify-backuper/
RUN go mod download -x

ADD . .

RUN go build -v -installsuffix cgo -ldflags="-w -s" -o /go/bin/inotify-backuper .


FROM debian:buster

ENV PATH='/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin'
CMD /go/bin/inotify-backuper

COPY --from=0 /go/bin /go/bin

