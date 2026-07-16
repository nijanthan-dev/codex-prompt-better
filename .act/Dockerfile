FROM golang:1.24-bookworm

ARG GITLEAKS_VERSION=8.30.1
ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
    && apt-get install --yes --no-install-recommends \
        ca-certificates \
        gcc \
        git \
        jq \
        libc6-dev \
        make \
    && GOBIN=/usr/local/bin go install github.com/zricethezav/gitleaks/v8@v${GITLEAKS_VERSION} \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/* /go/pkg/mod /root/.cache/go-build

ENV CGO_ENABLED=1 \
    GOTOOLCHAIN=local \
    GOPROXY=off

WORKDIR /workspace
