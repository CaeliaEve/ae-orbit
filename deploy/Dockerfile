FROM golang:1.27.3-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY oc ./oc
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ae-orbit ./cmd/ae-orbit

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/ae-orbit /usr/local/bin/ae-orbit
USER 65532:65532
ENV AE_LISTEN=0.0.0.0:8080
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/ae-orbit"]
CMD ["serve"]
