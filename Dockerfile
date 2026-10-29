FROM node:22.20.0-bookworm-slim AS web
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26.10-bookworm AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/ ./cmd/api ./cmd/worker ./cmd/migrate ./cmd/ops ./cmd/investigation-setup ./cmd/history-setup ./cmd/paypal-webhooks ./cmd/eval

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=go /out/ /app/bin/
COPY --from=web /src/web/dist/ /app/web/dist/
COPY models/*.json /app/models/
COPY search/ /app/search/
COPY datasets/splits.manifest.json /app/datasets/splits.manifest.json
COPY tests/fixtures/ /app/tests/fixtures/
USER 65532:65532
ENV HTTP_ADDR=0.0.0.0:8080 WEB_DIST_DIR=/app/web/dist
EXPOSE 8080
CMD ["/app/bin/api"]
