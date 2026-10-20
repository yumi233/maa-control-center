FROM scratch
COPY bin/maa-server-linux-amd64 /maa-server
USER 65532:65532
EXPOSE 18443
ENTRYPOINT ["/maa-server", "--config-file", "/config/server.private.json"]
