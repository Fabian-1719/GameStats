FROM sbtscala/scala-sbt:eclipse-temurin-21.0.12_8_1.13.0_2.13.18 AS build
WORKDIR /workspace
COPY build.sbt ./
COPY project ./project
COPY src ./src
RUN sbt assembly

FROM eclipse-temurin:21-jre-jammy AS runtime
WORKDIR /app
RUN apt-get update \
    && apt-get install -y --no-install-recommends curl \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /workspace/target/scala-3.3.6/scala-analytics.jar ./scala-analytics.jar
EXPOSE 8081
HEALTHCHECK --interval=30s --timeout=3s --start-period=20s --retries=3 \
    CMD curl -fsS http://127.0.0.1:8081/health || exit 1
ENTRYPOINT ["java", "-jar", "/app/scala-analytics.jar"]