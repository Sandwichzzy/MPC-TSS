FROM golang:1.25 AS builder

WORKDIR /src

ARG GOPROXY=https://goproxy.cn,https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    for i in 1 2 3; do \
        go mod download && break; \
        if [ "$i" = "3" ]; then exit 1; fi; \
        sleep 5; \
    done

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 GO111MODULE=on go build -o /out/tss .

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=builder /out/tss /usr/local/bin/tss

ENTRYPOINT ["tss"]
