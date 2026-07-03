# Multi-stage build producing a minimal, distroless final image
# (Technical Roadmap.md §9). Mirrors services/collector/Dockerfile's
# build-local go.work approach.
FROM golang:1.25-alpine AS build
WORKDIR /src

COPY shared/ shared/
COPY services/alerting-service/ services/alerting-service/
RUN printf 'go 1.25.0\n\nuse (\n\t./shared\n\t./services/alerting-service\n)\n' > go.work

WORKDIR /src/services/alerting-service
RUN go build -o /out/alerting-service ./cmd

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/alerting-service /alerting-service
ENTRYPOINT ["/alerting-service"]
