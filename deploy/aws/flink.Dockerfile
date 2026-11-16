ARG EMR_FLINK_IMAGE
FROM ${EMR_FLINK_IMAGE}
COPY sales-analytics.jar /opt/flink/usrlib/analytics.jar
# The IAM callback handler extends Kafka client classes, which live in the job jar.
# Keep both in usrlib so the user classloader resolves them together.
COPY aws-msk-iam-auth.jar /opt/flink/usrlib/aws-msk-iam-auth.jar
