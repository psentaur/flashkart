ARG EMR_FLINK_IMAGE
FROM ${EMR_FLINK_IMAGE}
COPY sales-analytics.jar /opt/flink/usrlib/analytics.jar
COPY aws-msk-iam-auth.jar /opt/flink/lib/aws-msk-iam-auth.jar
