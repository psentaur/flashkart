FROM node:22 AS build
WORKDIR /web
COPY web/package*.json ./
RUN npm ci
COPY web/src ./src
COPY web/index.html web/tsconfig.json ./
ARG VITE_AUTH_MODE=clerk
ARG VITE_CLERK_PUBLISHABLE_KEY
ENV VITE_AUTH_MODE=$VITE_AUTH_MODE VITE_CLERK_PUBLISHABLE_KEY=$VITE_CLERK_PUBLISHABLE_KEY
RUN test "$VITE_AUTH_MODE" = demo || test -n "$VITE_CLERK_PUBLISHABLE_KEY"
RUN npm run build

FROM nginx:1.28-alpine
COPY --from=build /web/dist /usr/share/nginx/html
COPY deploy/kubernetes/base/nginx.conf /etc/nginx/conf.d/default.conf
