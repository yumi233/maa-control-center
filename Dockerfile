FROM alpine:3.23 AS certificates
RUN apk add --no-cache ca-certificates
FROM scratch
COPY --from=certificates /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY bin/maa-server-linux-amd64 /maa-server
USER 65532:65532
EXPOSE 18443
ENTRYPOINT ["/maa-server", "--config-file", "/config/server.private.json"]
