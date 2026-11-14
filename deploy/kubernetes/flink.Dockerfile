FROM flink:1.20.0-scala_2.12-java17
ADD --checksum=sha256:324e6b28b8907c205b231910b14f4393cb7df214df1eafc3c03ac14630e39a33 https://repo.maven.apache.org/maven2/org/apache/flink/flink-metrics-prometheus/1.20.0/flink-metrics-prometheus-1.20.0.jar /opt/flink/lib/flink-metrics-prometheus-1.20.0.jar
COPY analytics/target/sales-analytics-0.1.0.jar /opt/flink/usrlib/analytics.jar
