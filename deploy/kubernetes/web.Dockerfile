FROM node:22 AS build
WORKDIR /web
COPY web/package*.json ./
RUN npm ci
COPY web/src ./src
COPY web/index.html web/tsconfig.json ./
RUN npm run build

FROM nginx:1.28-alpine
COPY --from=build /web/dist /usr/share/nginx/html
COPY deploy/kubernetes/base/nginx.conf /etc/nginx/conf.d/default.conf
