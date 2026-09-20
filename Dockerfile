FROM golang:1.27 AS build
WORKDIR /src
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /pulse .

# The binary is pure Go and makes no outbound calls, so it needs no libc, no
# shell and no CA bundle. 32767 matches the uid dokku's storage mount is
# created with.
FROM scratch
COPY --from=build /pulse /pulse
USER 32767:32767
EXPOSE 5000
ENTRYPOINT ["/pulse"]
