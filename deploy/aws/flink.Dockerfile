ARG EMR_FLINK_IMAGE
FROM ${EMR_FLINK_IMAGE}
COPY --chmod=0644 sales-analytics.jar /usr/lib/flink/usrlib/analytics.jar
