FROM golang:1.26.4-alpine AS go-builder
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}
WORKDIR /src
COPY go.mod go.sum ./
COPY backend ./backend
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/gateway ./backend/cmd/gateway
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/worker ./backend/cmd/worker
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/node-worker ./backend/cmd/node-worker
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/assops-tool ./backend/cmd/assops-tool

FROM alpine:3.24 AS go-runtime
WORKDIR /app
RUN apk add --no-cache ca-certificates git openssh-client postgresql-client kubectl sshpass
COPY --from=go-builder /out/assops-tool /usr/local/bin/assops-tool

FROM go-runtime AS gateway
COPY --from=go-builder /out/gateway /usr/local/bin/gateway
EXPOSE 8080
ENTRYPOINT ["gateway"]

FROM go-runtime AS worker
COPY --from=go-builder /out/worker /usr/local/bin/worker
ENTRYPOINT ["worker"]

FROM go-runtime AS node-worker
COPY --from=go-builder /out/node-worker /usr/local/bin/node-worker
ENTRYPOINT ["node-worker"]

FROM node:26-alpine AS web-builder
WORKDIR /src/web
RUN npm install -g pnpm@10
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web ./
RUN pnpm build

FROM nginx:1.31-alpine AS web
COPY deploy/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=web-builder /src/web/dist /usr/share/nginx/html
EXPOSE 80
