# Multi-stage build producing a minimal, distroless final image
FROM golang:1.25-alpine AS build
WORKDIR /src

COPY shared/ shared/
COPY services/anomaly-detector/ services/anomaly-detector/
RUN printf 'go 1.25.0\n\nuse (\n\t./shared\n\t./services/anomaly-detector\n)\n' > go.work

WORKDIR /src/services/anomaly-detector
RUN go build -o /out/anomaly-detector ./cmd

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/anomaly-detector /anomaly-detector
ENTRYPOINT ["/anomaly-detector"]
