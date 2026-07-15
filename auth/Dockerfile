FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /auth .

FROM alpine:3.21
RUN apk add --no-cache ffmpeg && addgroup -S app && adduser -S -G app app && mkdir -p /var/cache/cctv && chown app:app /var/cache/cctv
COPY --from=build /auth /usr/local/bin/auth
USER app
EXPOSE 3000
ENTRYPOINT ["/usr/local/bin/auth"]
