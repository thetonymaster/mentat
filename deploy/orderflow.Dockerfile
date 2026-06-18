FROM golang:1.25 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /bin/orderflow ./tracelab/orderflow/cmd/orderflow

FROM gcr.io/distroless/static-debian12
COPY --from=build /bin/orderflow /bin/orderflow
ENTRYPOINT ["/bin/orderflow"]
