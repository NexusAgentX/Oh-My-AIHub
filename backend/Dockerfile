FROM --platform=$BUILDPLATFORM golang:1.27.0-alpine AS build
ARG TARGETARCH

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
    && CGO_ENABLED=0 GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate \
    && CGO_ENABLED=0 GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/bootstrap-admin ./cmd/bootstrap-admin

FROM alpine:3.23
RUN addgroup -S app && adduser -S -G app app
COPY --from=build /out/server /out/migrate /out/bootstrap-admin /usr/local/bin/
USER app
EXPOSE 8080
CMD ["server"]
