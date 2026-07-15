FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /auth ./main.go

FROM alpine:3.21
RUN addgroup -S app && adduser -S -G app app
COPY --from=build /auth /usr/local/bin/auth
USER app
EXPOSE 3000
ENTRYPOINT ["/usr/local/bin/auth"]
