# Multi-stage build producing a minimal, distroless final image
# (Technical Roadmap.md §9). Mirrors services/collector/Dockerfile's
# build-local go.work approach (see that file's comment for the rationale).
FROM golang:1.25-alpine AS build
WORKDIR /src

COPY shared/ shared/
COPY services/mcp-gateway/ services/mcp-gateway/
RUN printf 'go 1.25.0\n\nuse (\n\t./shared\n\t./services/mcp-gateway\n)\n' > go.work

WORKDIR /src/services/mcp-gateway
RUN go build -o /out/mcp-gateway ./cmd

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/mcp-gateway /mcp-gateway
ENTRYPOINT ["/mcp-gateway"]
