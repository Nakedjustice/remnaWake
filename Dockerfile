# build stage
FROM golang:1.22-alpine AS build
WORKDIR /src

ENV CGO_ENABLED=0 GOOS=linux

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -ldflags="-s -w" -o /out/bot .

# runtime stage
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/bot /app/bot
USER nonroot:nonroot
ENTRYPOINT ["/app/bot"]
