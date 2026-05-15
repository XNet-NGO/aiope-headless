FROM golang:latest AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /aiope-headless .

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl git python3 build-essential openssh-client jq \
    && rm -rf /var/lib/apt/lists/* \
    && useradd -m -s /bin/bash -u 1001 aiope
COPY --from=build /aiope-headless /usr/local/bin/aiope-headless
ENV AIOPE_DB_PATH=/data/aiope2-chat.db
ENV HOME=/data
RUN mkdir -p /data && chown aiope:aiope /data && rm -rf /tmp && ln -s /data/tmp /tmp
VOLUME /data
EXPOSE 8090
USER aiope
ENTRYPOINT ["sh", "-c", "mkdir -p /data/tmp && exec aiope-headless"]
