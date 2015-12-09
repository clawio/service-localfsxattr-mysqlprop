FROM golang:1.5
MAINTAINER Hugo González Labrador

ENV CLAWIO_LOCALSTOREXATTRPROP_PORT 57003
ENV CLAWIO_LOCALSTOREXATTRPROP_DSN "prop:passforuserprop@tcp(service-localstorexattr-prop-mysql:3306)/prop"
ENV CLAWIO_SHAREDSECRET secret

ADD . /go/src/github.com/clawio/service.localstorexattr.prop
WORKDIR /go/src/github.com/clawio/service.localstorexattr.prop

RUN go get -u github.com/tools/godep
RUN godep restore
RUN go install

ENTRYPOINT /go/bin/service.localstorexattr.prop

EXPOSE 57003

