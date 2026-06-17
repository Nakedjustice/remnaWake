# build stage
FROM golang:1.25.11-alpine AS build
WORKDIR /src

ENV CGO_ENABLED=0 GOOS=linux

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -ldflags="-s -w" -o /out/bot .
RUN mkdir -p /data

# runtime stage
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/bot /app/bot
# Pre-create a nonroot-owned /data so the mounted named volume inherits
# writable ownership for the SQLite database.
COPY --from=build --chown=65532:65532 /data /data
USER nonroot:nonroot
ENTRYPOINT ["/app/bot"]
