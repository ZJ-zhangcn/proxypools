FROM --platform=$BUILDPLATFORM golang:1.22 AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -o /out/proxypools ./cmd/proxypools

FROM ghcr.io/sagernet/sing-box:v1.12.12
WORKDIR /app
COPY --from=build /out/proxypools /usr/local/bin/proxypools
EXPOSE 8080 7777 7780
ENTRYPOINT ["/usr/local/bin/proxypools"]
