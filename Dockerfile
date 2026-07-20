# App service: the Discovery Engine server (web UI + API).
# The daily pipeline runs from Dockerfile.cron.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/discovery ./cmd/discovery

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/discovery /usr/local/bin/discovery
WORKDIR /app
# Collections are baked in for file-mode users; db-mode deployments
# serve from the database and use these only as seeds.
COPY collections/ ./collections/
EXPOSE 8080
CMD ["discovery", "serve"]
