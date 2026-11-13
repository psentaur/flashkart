ARG APP_IMAGE
FROM ${APP_IMAGE} AS app
FROM public.ecr.aws/lambda/provided:al2023
COPY --from=app /usr/local/bin/notification /var/runtime/bootstrap
COPY config.json /var/task/config.json
ENTRYPOINT ["/var/runtime/bootstrap"]
