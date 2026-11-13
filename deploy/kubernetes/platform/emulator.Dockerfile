FROM debian:trixie-slim
COPY aws-emulator /usr/local/bin/aws-emulator
ENTRYPOINT ["/usr/local/bin/aws-emulator"]
