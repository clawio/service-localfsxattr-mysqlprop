FROM golang:1.5
MAINTAINER Hugo González Labrador

ENV CLAWIO_LOCALFSXATTR_MYSQLPROP_PORT 57013
ENV CLAWIO_LOCALFSXATTR_MYSQLPROP_DSN "prop:passforuserprop@tcp(service-localfsxattr-mysqlprop-mysql)/prop"
ENV CLAWIO_SHAREDSECRET secret

ADD . /go/src/github.com/clawio/service-localfsxattr-mysqlprop
WORKDIR /go/src/github.com/clawio/service-localfsxattr-mysqlprop

RUN go get -u github.com/tools/godep
RUN godep restore
RUN go install

ENTRYPOINT /go/bin/service-localfsxattr-mysqlprop

EXPOSE 57013

