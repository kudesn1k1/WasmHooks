FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/dataplane ./cmd/dataplane

# No shell and no curl in the final image: the compose healthcheck uses
# `dataplane probe <url>`. Wasm fixtures are not part of the image; modules
# come from S3.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/dataplane /dataplane
EXPOSE 8080 8081
ENTRYPOINT ["/dataplane"]
