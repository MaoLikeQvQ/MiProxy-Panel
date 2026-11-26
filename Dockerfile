ARG MIHOMO_IMAGE=metacubex/mihomo:v1.19.34
ARG NODE_IMAGE=node:24-alpine
ARG GO_IMAGE=golang:1.26-alpine

FROM ${MIHOMO_IMAGE} AS mihomo

FROM --platform=$BUILDPLATFORM ${NODE_IMAGE} AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum *.go ./
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/miproxy-panel .

FROM scratch
COPY --from=mihomo /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=mihomo /mihomo /usr/local/bin/mihomo
COPY --from=build /out/miproxy-panel /miproxy-panel
EXPOSE 8787
ENTRYPOINT ["/miproxy-panel"]
