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

ARG XIAOLI_SKILLS="mnhkahn/cyeam-cli https://open.feishu.cn"
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
    find /root /home -name 'skills-lock.json' -not -path '/skills/*' -exec cp {} /skills/ \; 2>/dev/null; \
    count=$(find /skills -name SKILL.md | wc -l); \
    echo "skills installed: $count"

ARG CYEAM_CLI_VERSION=latest

RUN set -eux; \
    mkdir -p /skill-bin; \
    find /skills -path '*/bin/*' -type f -perm /111 -exec cp -a {} /skill-bin/ \; ; \
    if [ ! -x /skill-bin/cyeam ]; then \
        if [ "${CYEAM_CLI_VERSION}" = "latest" ]; then \
            url="https://github.com/mnhkahn/cyeam-cli/releases/latest/download/cyeam_Linux_x86_64.tar.gz"; \
        else \
            url="https://github.com/mnhkahn/cyeam-cli/releases/download/${CYEAM_CLI_VERSION}/cyeam_Linux_x86_64.tar.gz"; \
        fi; \
        curl -fsSL -o /tmp/cyeam.tgz "$url"; \
        tar -xzf /tmp/cyeam.tgz -C /skill-bin cyeam; \
        rm /tmp/cyeam.tgz; \
    fi; \
    find /skill-bin -type f -exec chmod +x {} \;

ARG LARK_CLI_VERSION=latest

RUN set -eux; \
    if [ "${LARK_CLI_VERSION}" = "latest" ]; then \
        npm install -g @larksuite/cli; \
    else \
        npm install -g "@larksuite/cli@${LARK_CLI_VERSION}"; \
    fi; \
    larkbin="$(npm root -g)/@larksuite/cli/bin/lark-cli"; \
    test -x "$larkbin"; \
    cp -a "$larkbin" /skill-bin/lark-cli; \
    chmod +x /skill-bin/lark-cli

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates git libopus0 libopusfile0 libstdc++6 \
    && rm -rf /var/lib/apt/lists/*

COPY --from=onnxrt /opt/onnxruntime/lib/libonnxruntime.so* /usr/local/lib/
RUN ldconfig

COPY --from=go-build /out/xiaoli-admin /usr/local/bin/xiaoli-admin
COPY --from=skill-build /skill-bin/ /usr/local/bin/
COPY --from=skill-build /skills /opt/xiaoli/skills
COPY --from=skill-build /usr/local/bin/node /usr/local/bin/node
COPY --from=skill-build /usr/local/lib/node_modules /usr/local/lib/node_modules
RUN ln -sf /usr/local/lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
    && ln -sf /usr/local/lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx
RUN mkdir -p /root/.config && if [ -f /opt/xiaoli/skills/skills-lock.json ]; then cp /opt/xiaoli/skills/skills-lock.json /root/.config/skills-lock.json; fi
COPY AGENT.md /opt/xiaoli/AGENT.md
COPY settings.json /opt/xiaoli/settings.json
COPY agents/ /opt/xiaoli/agents/
COPY .cyeam/ /root/.cyeam/

WORKDIR /data

ENV XIAOLI_ADMIN_HOST=0.0.0.0
ENV XIAOLI_ADMIN_PORT=8080
ENV XIAOLI_DIRECT_DEVICE_SERVER=true
ENV XIAOLI_ONNXRUNTIME_PATH=/usr/local/lib/libonnxruntime.so

EXPOSE 8080

CMD ["/usr/local/bin/xiaoli-admin"]
