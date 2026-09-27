# The provider is a single static Go binary. terraform-provider-talos runs
# in-process (upjet plugin-framework client), so there is no terraform CLI,
# no provider plugin mirror and no TF_* environment.
FROM gcr.io/distroless/static-debian12:nonroot

ARG TARGETOS
ARG TARGETARCH

ADD "bin/${TARGETOS}_${TARGETARCH}/provider" /usr/local/bin/provider

USER 65532:65532
EXPOSE 8080

ENTRYPOINT ["provider"]
