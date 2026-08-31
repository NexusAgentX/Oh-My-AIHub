FROM golang:1.27.0-alpine AS build

WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /server ./cmd/server

FROM alpine:3.23
RUN addgroup -S app && adduser -S -G app app
COPY --from=build /server /usr/local/bin/server
USER app
EXPOSE 8080
CMD ["server"]
