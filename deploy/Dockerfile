# Runtime-only, zero build-time network. The static binary is built on the
# host (Makefile `up`) because in-container egress can't reach the host-local
# proxy in this environment. No `apk add`: v1 talks only to PG/Redis/NATS over
# plaintext on the internal network, so no extra packages are needed.
# CI/prod can switch to a multi-stage build once the builder has registry+
# module access.
FROM alpine:3.20
COPY bin/captain /usr/local/bin/captain
EXPOSE 8080
ENTRYPOINT ["captain"]
