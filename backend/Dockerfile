FROM --platform=$BUILDPLATFORM golang:1.27.0-alpine AS build
ARG TARGETARCH

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
    && CGO_ENABLED=0 GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate \
    && CGO_ENABLED=0 GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/cutover-check ./cmd/cutover-check

FROM alpine:3.24
LABEL io.oh-my-aihub.schema-epoch="23"
LABEL io.oh-my-aihub.fixed-test-admin="1"
RUN addgroup -S app && adduser -S -G app app
COPY --from=build /out/server /out/migrate /out/cutover-check /usr/local/bin/
USER app
EXPOSE 8080
CMD ["server"]
