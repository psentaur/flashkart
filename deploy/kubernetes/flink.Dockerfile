FROM flink:1.20.0-scala_2.12-java17
RUN cp /opt/flink/opt/flink-metrics-prometheus-*.jar /opt/flink/lib/
COPY analytics/target/sales-analytics-0.1.0.jar /opt/flink/usrlib/analytics.jar
