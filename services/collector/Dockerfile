# Multi-stage build producing a minimal, distroless final image
# (Technical Roadmap.md §9: "multi-stage Dockerfiles producing distroless
# final images").
FROM golang:1.25-alpine AS build
WORKDIR /src

# Copy only this service's module plus the shared module it depends on —
# not the repo's own go.work, which lists every workspace member (cli,
# query-api, ...) and would fail to resolve inside a build context that
# only has two of those four directories. A minimal, build-local go.work
# scoped to exactly what this image needs keeps the build context small
# and the failure mode obvious (a real `go: cannot load module` error was
# hit and fixed here, not merely anticipated).
COPY shared/ shared/
COPY services/collector/ services/collector/
RUN printf 'go 1.25.0\n\nuse (\n\t./shared\n\t./services/collector\n)\n' > go.work

WORKDIR /src/services/collector
RUN go build -o /out/collector ./cmd

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/collector /collector
ENTRYPOINT ["/collector"]
