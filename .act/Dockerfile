FROM golang:1.24-bookworm AS go-toolchain

FROM postgres:17-bookworm

COPY --from=go-toolchain /usr/local/go /usr/local/go

ARG GITLEAKS_VERSION=8.30.1
ENV DEBIAN_FRONTEND=noninteractive
ENV PATH=/usr/local/go/bin:${PATH}

RUN apt-get update \
    && apt-get install --yes --no-install-recommends \
        ca-certificates \
        gcc \
        git \
        jq \
        libc6-dev \
        make \
        postgresql-client-16 \
    && GOBIN=/usr/local/bin go install github.com/zricethezav/gitleaks/v8@v${GITLEAKS_VERSION} \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/* /root/.cache/go-build

COPY go.mod go.sum /tmp/prompt-better-modules/
RUN cd /tmp/prompt-better-modules && go mod download

ENV CGO_ENABLED=1 \
    GOTOOLCHAIN=local \
    GOPROXY=off

WORKDIR /workspace
