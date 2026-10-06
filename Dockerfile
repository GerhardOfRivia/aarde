FROM node:22-bookworm-slim AS frontend
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
COPY internal/api/openapi.json /src/internal/api/openapi.json
RUN npm run build

FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /src/web/dist ./web/dist

FROM build AS test
ENV AARDE_REQUIRE_GDAL=true
RUN apt-get update && apt-get install -y --no-install-recommends gdal-bin && gdalinfo --format NITF && gdalinfo --format JPEG && gdalinfo --format JP2OpenJPEG && rm -rf /var/lib/apt/lists/*
CMD ["go", "test", "-race", "-count=1", "-v", "./..."]

FROM build AS binary
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/aarde ./cmd/aarde

FROM debian:bookworm-slim AS runtime
RUN apt-get update && apt-get install -y --no-install-recommends gdal-bin ca-certificates \
    && gdalinfo --format NITF && gdalinfo --format JPEG && gdalinfo --format JP2OpenJPEG \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 aarde && useradd --uid 10001 --gid aarde --create-home aarde
COPY --from=binary /out/aarde /usr/local/bin/aarde
USER aarde
WORKDIR /home/aarde
# Viewer buffers use disposable /tmp; configure a bounded tmpfs in deployment.
ENV GDAL_CACHEMAX=64 GDAL_NUM_THREADS=1
ENV AARDE_WEB_TOKEN_PATH=/home/aarde/.local/state/aarde/web.token
EXPOSE 8080
ENTRYPOINT ["aarde"]
CMD ["serve"]
