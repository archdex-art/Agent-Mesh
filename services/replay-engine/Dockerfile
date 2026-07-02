# See services/collector/Dockerfile for the multi-stage build rationale
# and the minimal-go.work-per-image pattern; identical, only the built
# service and its module dependency differ.
FROM golang:1.25-alpine AS build
WORKDIR /src

COPY shared/ shared/
COPY services/replay-engine/ services/replay-engine/
RUN printf 'go 1.25.0\n\nuse (\n\t./shared\n\t./services/replay-engine\n)\n' > go.work

WORKDIR /src/services/replay-engine
RUN go build -o /out/replay-engine ./cmd

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/replay-engine /replay-engine
ENTRYPOINT ["/replay-engine"]
