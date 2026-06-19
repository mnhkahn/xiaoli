FROM golang:1.25-bookworm AS go-build

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        gcc \
        libc6-dev \
        libopus-dev \
        libopusfile-dev \
        pkg-config \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
ENV CGO_ENABLED=1
RUN go build -o /out/xiaoli-admin ./cmd/xiaoli-admin

FROM debian:bookworm-slim AS onnxrt

ARG ONNXRUNTIME_VERSION=1.25.1
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl tar \
    && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL -o /tmp/ort.tgz \
        "https://github.com/microsoft/onnxruntime/releases/download/v${ONNXRUNTIME_VERSION}/onnxruntime-linux-x64-${ONNXRUNTIME_VERSION}.tgz" \
    && mkdir -p /opt/onnxruntime \
    && tar -xzf /tmp/ort.tgz -C /opt/onnxruntime --strip-components=1 \
    && rm /tmp/ort.tgz

FROM node:22-bookworm-slim AS skill-build

ARG XIAOLI_SKILLS="mnhkahn/cyeam-cli"
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl git tar \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /skill-src
RUN set -eux; \
    mkdir -p /skills; \
    for skill in $XIAOLI_SKILLS; do \
        npx -y skills add "$skill" -- -y -g; \
    done; \
    find /root /home -name SKILL.md -not -path '/skills/*' -print 2>/dev/null | while read -r skill_file; do \
        skill_dir="$(dirname "$skill_file")"; \
        skill_name="$(basename "$skill_dir")"; \
        rm -rf "/skills/$skill_name"; \
        cp -a "$skill_dir" "/skills/$skill_name"; \
    done; \
    count=$(find /skills -name SKILL.md | wc -l); \
    echo "skills installed: $count"

RUN set -eux; \
    mkdir -p /skill-bin; \
    find /skills -path '*/bin/*' -type f -perm /111 -exec cp -a {} /skill-bin/ \; ; \
    if [ ! -x /skill-bin/cyeam ] && [ -d /skills/cyeam-cli ]; then \
        curl -fsSL -o /tmp/cyeam.tgz https://github.com/mnhkahn/cyeam-cli/releases/latest/download/cyeam_Linux_x86_64.tar.gz; \
        tar -xzf /tmp/cyeam.tgz -C /skill-bin cyeam; \
        rm /tmp/cyeam.tgz; \
    fi; \
    find /skill-bin -type f -exec chmod +x {} \;

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates libopus0 libopusfile0 libstdc++6 \
    && rm -rf /var/lib/apt/lists/*

COPY --from=onnxrt /opt/onnxruntime/lib/libonnxruntime.so* /usr/local/lib/
RUN ldconfig

COPY --from=go-build /out/xiaoli-admin /usr/local/bin/xiaoli-admin
COPY --from=skill-build /skill-bin/ /usr/local/bin/
COPY --from=skill-build /skills /opt/xiaoli/skills
COPY AGENT.md /opt/xiaoli/AGENT.md
COPY settings.json /opt/xiaoli/settings.json

WORKDIR /data

ENV XIAOLI_ADMIN_HOST=0.0.0.0
ENV XIAOLI_ADMIN_PORT=8080
ENV XIAOLI_DIRECT_DEVICE_SERVER=true
ENV XIAOLI_ONNXRUNTIME_PATH=/usr/local/lib/libonnxruntime.so

EXPOSE 8080

CMD ["/usr/local/bin/xiaoli-admin"]
