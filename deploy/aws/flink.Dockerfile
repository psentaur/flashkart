ARG EMR_FLINK_IMAGE
FROM ${EMR_FLINK_IMAGE}
COPY --chmod=0644 sales-analytics.jar /usr/lib/flink/usrlib/analytics.jar
COPY --chmod=0644 aws-msk-iam-auth.jar /usr/lib/flink/lib/aws-msk-iam-auth.jar
