ARG APP_IMAGE
FROM ${APP_IMAGE} AS app
FROM public.ecr.aws/lambda/provided:al2023
COPY --from=app /usr/local/bin/notification /var/runtime/bootstrap
COPY --chmod=0644 config.json /var/task/config.json
ENTRYPOINT ["/var/runtime/bootstrap"]
