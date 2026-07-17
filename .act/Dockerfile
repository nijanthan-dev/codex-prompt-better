FROM golang:1.25.12-bookworm@sha256:ea341baa9bd5ba6784f6d7161ace70544349a6242d54d34a0fbfd2c4d51c9d58 AS go-toolchain

FROM postgres:17-bookworm@sha256:4f736ae292687621d4dbe0d499ffd024a36bd2ee7d8ca6f2ccd4c800f047b394

COPY --from=go-toolchain /usr/local/go /usr/local/go

ARG GITLEAKS_VERSION=8.30.1
ARG GOVULNCHECK_VERSION=1.6.0
ARG GORELEASER_VERSION=2.17.0
ARG SYFT_VERSION=1.48.0
ARG TARGETARCH
ARG VULNDB_REFRESH
ENV DEBIAN_FRONTEND=noninteractive
ENV PATH=/usr/local/go/bin:${PATH}

RUN apt-get update \
    && apt-get install --yes --no-install-recommends \
        ca-certificates \
        curl \
        gcc \
        git \
        jq \
        libc6-dev \
        make \
        postgresql-client-16 \
        shellcheck \
        unzip \
    && GOBIN=/usr/local/bin go install github.com/zricethezav/gitleaks/v8@v${GITLEAKS_VERSION} \
    && GOBIN=/usr/local/bin go install golang.org/x/vuln/cmd/govulncheck@v${GOVULNCHECK_VERSION} \
    && case "${TARGETARCH}" in arm64) goreleaser_arch=arm64 ;; amd64) goreleaser_arch=x86_64 ;; *) exit 1 ;; esac \
    && curl --fail --location --proto '=https' --tlsv1.2 \
        --output /tmp/goreleaser.tar.gz \
        "https://github.com/goreleaser/goreleaser/releases/download/v${GORELEASER_VERSION}/goreleaser_Linux_${goreleaser_arch}.tar.gz" \
    && curl --fail --location --proto '=https' --tlsv1.2 \
        --output /tmp/goreleaser-checksums.txt \
        "https://github.com/goreleaser/goreleaser/releases/download/v${GORELEASER_VERSION}/checksums.txt" \
    && cd /tmp \
    && grep "  goreleaser_Linux_${goreleaser_arch}.tar.gz$" goreleaser-checksums.txt \
        | sed "s#goreleaser_Linux_${goreleaser_arch}.tar.gz#goreleaser.tar.gz#" \
        | sha256sum --check - \
    && tar -xzf /tmp/goreleaser.tar.gz -C /usr/local/bin goreleaser \
    && curl --fail --location --proto '=https' --tlsv1.2 \
        --output "/tmp/syft_${SYFT_VERSION}_linux_${TARGETARCH}.tar.gz" \
        "https://github.com/anchore/syft/releases/download/v${SYFT_VERSION}/syft_${SYFT_VERSION}_linux_${TARGETARCH}.tar.gz" \
    && curl --fail --location --proto '=https' --tlsv1.2 \
        --output /tmp/syft-checksums.txt \
        "https://github.com/anchore/syft/releases/download/v${SYFT_VERSION}/syft_${SYFT_VERSION}_checksums.txt" \
    && cd /tmp \
    && grep "  syft_${SYFT_VERSION}_linux_${TARGETARCH}.tar.gz$" syft-checksums.txt \
        | sha256sum --check - \
    && tar -xzf "/tmp/syft_${SYFT_VERSION}_linux_${TARGETARCH}.tar.gz" -C /usr/local/bin syft \
    && echo "Go vulnerability database snapshot: ${VULNDB_REFRESH}" \
    && curl --fail --location --proto '=https' --tlsv1.2 \
        --output /tmp/vulndb.zip https://vuln.go.dev/vulndb.zip \
    && unzip -Z1 /tmp/vulndb.zip > /tmp/vulndb-files \
    && ! grep -Eq '(^/|(^|/)\.\.(/|$)|\\)' /tmp/vulndb-files \
    && grep -qx 'index/db.json' /tmp/vulndb-files \
    && grep -qx 'index/modules.json' /tmp/vulndb-files \
    && mkdir -p /opt/go-vulndb \
    && unzip -q /tmp/vulndb.zip -d /opt/go-vulndb \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/* /root/.cache/go-build /tmp/*.tar.gz /tmp/*.zip /tmp/*checksums.txt /tmp/vulndb-files

COPY . /tmp/prompt-better-modules/
RUN cd /tmp/prompt-better-modules \
    && cp go.mod /tmp/prompt-better-go.mod \
    && cp go.sum /tmp/prompt-better-go.sum \
    && go mod tidy \
    && cmp go.mod /tmp/prompt-better-go.mod \
    && cmp go.sum /tmp/prompt-better-go.sum

ENV CGO_ENABLED=1 \
    GOTOOLCHAIN=local

WORKDIR /workspace
