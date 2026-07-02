# Multi-stage build producing a minimal, distroless final image
# (Technical Roadmap.md §9). Mirrors services/collector/Dockerfile's
# build-local go.work approach.
FROM golang:1.25-alpine AS build
WORKDIR /src

COPY shared/ shared/
COPY services/jobs/ services/jobs/
RUN printf 'go 1.25.0\n\nuse (\n\t./shared\n\t./services/jobs\n)\n' > go.work

WORKDIR /src/services/jobs
RUN go build -o /out/jobs ./cmd

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/jobs /jobs
ENTRYPOINT ["/jobs"]
