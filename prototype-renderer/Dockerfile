FROM alpine:3.24 AS dependencies
RUN apk add --no-cache npm
WORKDIR /opt/prototype-renderer
COPY package.json package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts --no-audit --no-fund

FROM alpine:3.24
RUN apk add --no-cache chromium font-noto-cjk nodejs \
    && mkdir -p /run/prototype-renderer
WORKDIR /opt/prototype-renderer
COPY --from=dependencies /opt/prototype-renderer/node_modules ./node_modules
COPY server.cjs ./
ENV HOME=/tmp \
    CHROMIUM_PATH=/usr/bin/chromium
USER 65532:65532
CMD ["node", "server.cjs"]
