FROM docker.io/library/golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/datacat ./cmd/datacat

FROM docker.io/library/alpine:3.22
RUN addgroup -S datacat && adduser -S -G datacat -H -s /sbin/nologin datacat
COPY --from=build /out/datacat /usr/local/bin/datacat
USER datacat
EXPOSE 8080
# Forme exec : le binaire est PID 1 et reçoit SIGTERM directement.
# Pas de HEALTHCHECK : ignoré par le format OCI, défini dans compose.yaml.
ENTRYPOINT ["/usr/local/bin/datacat"]
