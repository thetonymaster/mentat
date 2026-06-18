FROM golang:1.25 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /bin/orderflow ./tracelab/orderflow/cmd/orderflow

# Pinned to the nonroot variant by immutable digest (resolved from the multi-arch
# :nonroot tag) for reproducible builds. Refresh the digest when intentionally
# bumping the base image.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:d093aa3e30dbadd3efe1310db061a14da60299baff8450a17fe0ccc514a16639
COPY --from=build /bin/orderflow /bin/orderflow
USER nonroot:nonroot
ENTRYPOINT ["/bin/orderflow"]
