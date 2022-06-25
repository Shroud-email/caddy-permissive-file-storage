# Compile caddy with custom modules
FROM caddy:2.4.5-builder as builder

ENV CADDY_VERSION=v2.4.5
RUN xcaddy build \
    --with github.com/Shroud-email/caddy-permissive-file-storage

# Production image
FROM caddy:2.4.5-alpine

COPY --from=builder /usr/bin/caddy /usr/bin/caddy
VOLUME /etc/caddy/Caddyfile
