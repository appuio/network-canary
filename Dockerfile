FROM gcr.io/distroless/static:nonroot

WORKDIR /
COPY network-canary canary
USER 65532:65532

CMD ["/canary"]
