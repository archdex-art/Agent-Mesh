# Multi-stage build producing a minimal, distroless final image
# (Technical Roadmap.md §9: "multi-stage Dockerfiles producing distroless
# final images"). Mirrors services/collector/Dockerfile's build-local
# go.work approach (see that file's comment for the rationale).
FROM golang:1.25-alpine AS build
WORKDIR /src

COPY shared/ shared/
COPY services/realtime-gateway/ services/realtime-gateway/
RUN printf 'go 1.25.0\n\nuse (\n\t./shared\n\t./services/realtime-gateway\n)\n' > go.work

WORKDIR /src/services/realtime-gateway
RUN go build -o /out/realtime-gateway ./cmd

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/realtime-gateway /realtime-gateway
ENTRYPOINT ["/realtime-gateway"]
