# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src

ENV CGO_ENABLED=0 \
    GOTOOLCHAIN=auto

COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./
COPY web ./web

RUN echo "Building Keshk for ${TARGETOS:-linux}/${TARGETARCH:-amd64}" && \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" -o /out/keshk .

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app

COPY --from=build /out/keshk /app/keshk

ENV PORT=3000 \
    TZ=Europe/Belgrade

EXPOSE 3000

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD ["/app/keshk", "-healthcheck"]

USER nonroot:nonroot
ENTRYPOINT ["/app/keshk"]
