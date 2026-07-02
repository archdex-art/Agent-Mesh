# See services/collector/Dockerfile for the multi-stage build rationale
# and the minimal-go.work-per-image pattern; identical, only the built
# service and its module dependency differ.
FROM golang:1.25-alpine AS build
WORKDIR /src

COPY shared/ shared/
COPY services/query-api/ services/query-api/
RUN printf 'go 1.25\n\nuse (\n\t./shared\n\t./services/query-api\n)\n' > go.work

WORKDIR /src/services/query-api
RUN go build -o /out/query-api ./cmd

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/query-api /query-api
ENTRYPOINT ["/query-api"]
