# syntax=docker/dockerfile:1

FROM golang:1.27-bookworm AS build
WORKDIR /src

COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go test ./...
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/gateway ./cmd/gateway
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/devpreview ./cmd/devpreview

FROM scratch AS binaries
COPY --from=build /out/ /

FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=build /out/gateway /usr/local/bin/gateway
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/gateway"]
